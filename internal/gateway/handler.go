// Package gateway orchestrates the /v1 request pipeline: authentication,
// rate limiting, DLP, quota, upstream proxying, settlement, audit, and alerts.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/alert"
	"github.com/company/api-key/internal/audit"
	"github.com/company/api-key/internal/auth"
	"github.com/company/api-key/internal/dlp"
	"github.com/company/api-key/internal/proxy"
	"github.com/company/api-key/internal/quota"
	"github.com/company/api-key/internal/store"
)

// DefaultMaxBodyBytes is the request body limit.
const DefaultMaxBodyBytes int64 = 2 * 1024 * 1024

const (
	maxModelLength     = 128
	settlementTimeout  = 5 * time.Second
	clientClosedStatus = 499
)

// Handler runs the gateway request pipeline.
type Handler struct {
	deps    HandlerDeps
	maxBody int64
	logger  *slog.Logger
	now     func() time.Time
}

// New creates a gateway handler.
func New(deps HandlerDeps) *Handler {
	maxBody := deps.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Clock
	if now == nil {
		now = time.Now
	}
	return &Handler{deps: deps, maxBody: maxBody, logger: logger, now: now}
}

type requestState struct {
	event audit.Event
}

type requestMeta struct {
	Model     string
	MaxTokens int64
	Stream    bool
}

// Handle processes one gateway request. Every outcome is recorded in exactly
// one audit event; audit and alert enqueueing never wait for persistence.
func (h *Handler) Handle(c *gin.Context) {
	start := h.now()
	reqID := requestID()
	c.Header(RequestIDHeader, reqID)
	if clientID := clientRequestID(c.Request); clientID != "" {
		c.Header(ClientRequestIDHeader, clientID)
	}

	st := &requestState{event: audit.Event{
		RequestID: reqID,
		EventTime: start,
		ClientIP:  auth.ClientIP(c.Request, h.deps.TrustedProxies),
		Method:    c.Request.Method,
		Path:      c.Request.URL.Path,
		DLPAction: "none",
		DLPHits:   nil,
	}}
	defer h.enqueueAudit(st, start)

	if h.deps.Auth == nil {
		h.misconfigured(c, st, "authenticator is not configured")
		return
	}
	principal, err := h.deps.Auth.Authenticate(c.Request)
	if err != nil {
		status, responseCode, auditCode := authFailure(err)
		h.fail(c, st, status, responseCode, authMessage(status), nil)
		st.event.ErrorCode = auditCode
		return
	}
	h.recordPrincipal(st, principal)

	if h.deps.Limiter != nil && principal.Token.RateLimitRPM > 0 {
		allowed, retryAfter, err := h.deps.Limiter.Allow(
			c.Request.Context(), principal.Token.ID.String(), principal.Token.RateLimitRPM)
		switch {
		case err != nil:
			// API traffic is fail-open when both Redis and the in-process
			// fallback fail; the limiter exposes degradation for alerting.
			h.logger.Warn("rate limiter degraded; allowing request",
				"request_id", reqID, "error", err)
		case !allowed:
			c.Header("Retry-After", retryAfterHeader(retryAfter))
			h.fail(c, st, http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded", nil)
			return
		}
	}

	body, err := h.readBody(c)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.fail(c, st, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large", nil)
			return
		}
		if c.Request.Context().Err() != nil {
			st.event.ClientDisconnected = true
			st.event.StatusCode = clientClosedStatus
			return
		}
		h.fail(c, st, http.StatusBadRequest, "invalid_request", "Unable to read request body", nil)
		return
	}
	// Prompt keeps the original bytes for investigation; only the forwarded
	// copy is masked by DLP.
	st.event.Prompt = body

	meta := parseRequestMeta(body)
	st.event.Model = meta.Model
	if len(meta.Model) > maxModelLength {
		h.fail(c, st, http.StatusBadRequest, "invalid_request", "Model name is too long", nil)
		return
	}

	if _, ok := proxy.ProtocolForPath(c.Request.URL.Path); !ok {
		h.fail(c, st, http.StatusNotImplemented, "unsupported_endpoint", "Endpoint is not supported", nil)
		return
	}

	if h.deps.Resolver == nil {
		h.misconfigured(c, st, "resolver is not configured")
		return
	}
	target, err := h.deps.Resolver.Resolve(c.Request.URL.Path, meta.Model)
	if err != nil {
		if errors.Is(err, proxy.ErrUnsupportedEndpoint) {
			h.fail(c, st, http.StatusNotImplemented, "unsupported_endpoint", "Endpoint is not supported", nil)
			return
		}
		h.fail(c, st, http.StatusBadRequest, "no_route", "No route is configured for this request", nil)
		return
	}
	providerID := target.Provider.ID
	st.event.ProviderID = &providerID

	if h.deps.DLP == nil {
		h.misconfigured(c, st, "DLP engine is not configured")
		return
	}
	inspection, err := h.deps.DLP.Inspect(body)
	if err != nil {
		switch {
		case errors.Is(err, dlp.ErrUnsupportedContent):
			h.fail(c, st, http.StatusUnsupportedMediaType, "unsupported_media", "Only text requests are supported", nil)
		case errors.Is(err, dlp.ErrDuplicateKeys), errors.Is(err, dlp.ErrTooManyMatches):
			h.fail(c, st, http.StatusBadRequest, "invalid_request", "Request content cannot be inspected safely", nil)
		default:
			h.logger.Error("DLP inspection failed", "request_id", reqID, "error", err)
			h.fail(c, st, http.StatusInternalServerError, "internal_error", "Internal gateway error", nil)
		}
		return
	}
	action := inspection.Action
	if action == "" {
		action = "none"
	}
	st.event.DLPAction = action
	st.event.DLPHits = inspection.Hits
	h.enqueueAlerts(c.Request.Context(), principal, reqID, inspection.Hits)

	forwardBody := body
	switch action {
	case "none", "audit":
	case "mask":
		if len(inspection.Body) == 0 && len(body) > 0 {
			// Fail closed rather than forwarding content that should have
			// been rewritten.
			h.logger.Error("DLP mask returned an empty body", "request_id", reqID)
			h.fail(c, st, http.StatusInternalServerError, "internal_error", "Internal gateway error", nil)
			return
		}
		forwardBody = inspection.Body
	case "block":
		h.fail(c, st, http.StatusForbidden, "dlp_blocked", "Request blocked by data loss prevention policy",
			uniqueRuleNames(inspection.Hits))
		return
	default:
		h.logger.Error("DLP returned an unknown action", "request_id", reqID, "action", action)
		h.fail(c, st, http.StatusInternalServerError, "internal_error", "Internal gateway error", nil)
		return
	}
	if action == "mask" {
		// A masked model name must not be replaced later by the proxy's
		// original upstream-model rewrite.
		maskedModel := parseRequestMeta(forwardBody).Model
		if maskedModel != meta.Model {
			target.UpstreamModel = maskedModel
		}
	}

	endpoint := classifyEndpoint(c.Request.URL.Path, c.Request.Method)
	var reservation store.Reservation
	reserved := false
	if endpoint != quota.EndpointMetadata {
		if h.deps.Quota == nil {
			h.misconfigured(c, st, "quota service is not configured")
			return
		}
		reservation, err = h.deps.Quota.Reserve(c.Request.Context(), &principal.Token, endpoint, meta.MaxTokens)
		if err != nil {
			if errors.Is(err, store.ErrQuotaExceeded) {
				h.fail(c, st, http.StatusPaymentRequired, "quota_exceeded", "Quota exceeded", nil)
				return
			}
			h.logger.Error("quota reservation failed", "request_id", reqID, "error", err)
			h.fail(c, st, http.StatusServiceUnavailable, "quota_unavailable", "Quota service unavailable", nil)
			return
		}
		reserved = true
	}

	if h.deps.Upstream == nil {
		h.misconfigured(c, st, "upstream proxy is not configured")
		return
	}
	upstreamReq := proxy.Request{
		Target:    target,
		Path:      c.Request.URL.Path,
		RawQuery:  c.Request.URL.RawQuery,
		Body:      forwardBody,
		Header:    c.Request.Header,
		RequestID: reqID,
		Stream:    meta.Stream,
	}

	upstreamStart := h.now()
	var result proxy.Result
	var proxyErr error
	if meta.Stream {
		result, proxyErr = h.deps.Upstream.Stream(
			c.Request.Context(), c.Writer, c.Writer.Flush, upstreamReq)
	} else {
		result, proxyErr = h.deps.Upstream.Do(c.Request.Context(), c.Writer, upstreamReq)
	}
	st.event.UpstreamLatencyMS = int(h.now().Sub(upstreamStart).Milliseconds())
	h.recordResult(st, result)

	if reserved {
		h.settle(principal, endpoint, reservation, result.Usage, reqID)
	}

	if proxyErr != nil {
		h.proxyFailure(c, st, result, proxyErr)
		return
	}
	if result.StatusCode == 0 {
		h.fail(c, st, http.StatusBadGateway, "upstream_unavailable", "Upstream provider is unavailable", nil)
		return
	}
	st.event.StatusCode = result.StatusCode
	if result.StatusCode >= http.StatusBadRequest {
		st.event.ErrorCode = "upstream_error"
	}
}

func (h *Handler) readBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBody)
	return io.ReadAll(reader)
}

func (h *Handler) recordPrincipal(st *requestState, principal *auth.Principal) {
	tokenID := principal.Token.ID
	employeeID := principal.Employee.ID
	st.event.TokenID = &tokenID
	st.event.EmployeeID = &employeeID
	if principal.Employee.DepartmentID != nil {
		departmentID := *principal.Employee.DepartmentID
		st.event.DepartmentID = &departmentID
	}
}

func (h *Handler) recordResult(st *requestState, result proxy.Result) {
	if result.StatusCode != 0 {
		st.event.StatusCode = result.StatusCode
	}
	st.event.FirstByteMS = result.FirstByteMS
	st.event.UpstreamRequestID = result.UpstreamRequestID
	st.event.PromptTokens = result.Usage.PromptTokens
	st.event.CompletionTokens = result.Usage.CompletionTokens
	st.event.TotalTokens = result.Usage.TotalTokens
	st.event.UsageEstimated = result.Usage.Estimated
	st.event.Response = result.ResponseBody
	st.event.ResponseTruncated = result.ResponseTruncated
	st.event.ClientDisconnected = st.event.ClientDisconnected || result.ClientDisconnected
}

func (h *Handler) settle(principal *auth.Principal, endpoint quota.Endpoint, reservation store.Reservation, usage proxy.Usage, reqID string) {
	if h.deps.Quota == nil || reservation.TokenID == uuid.Nil {
		return
	}
	actual := settlementActual(&principal.Token, endpoint, reservation, usage)
	// Settlement must still complete after the client disconnects, so it is
	// detached from the request context.
	ctx, cancel := context.WithTimeout(context.Background(), settlementTimeout)
	defer cancel()
	if err := h.deps.Quota.Settle(ctx, reservation, actual); err != nil {
		h.logger.Error("quota settlement failed",
			"request_id", reqID, "token_id", reservation.TokenID, "error", err)
	}
}

func settlementActual(token *store.Token, endpoint quota.Endpoint, reservation store.Reservation, usage proxy.Usage) int64 {
	if token.QuotaType == "requests" {
		return 1
	}
	if endpoint == quota.EndpointMetadata {
		return 0
	}
	if !usage.Estimated && usage.TotalTokens > 0 {
		return int64(usage.TotalTokens)
	}
	return reservation.Reserved
}

func (h *Handler) proxyFailure(c *gin.Context, st *requestState, result proxy.Result, err error) {
	if errors.Is(err, proxy.ErrClientDisconnected) || result.ClientDisconnected {
		st.event.ClientDisconnected = true
		st.event.ErrorCode = "client_disconnected"
		if result.StatusCode == 0 {
			st.event.StatusCode = clientClosedStatus
		} else {
			st.event.StatusCode = result.StatusCode
		}
		return
	}

	status := http.StatusBadGateway
	code := "upstream_unavailable"
	switch {
	case errors.Is(err, proxy.ErrUpstreamTimeout):
		status, code = http.StatusGatewayTimeout, "upstream_timeout"
	case errors.Is(err, proxy.ErrUpstreamUnavailable):
		status, code = http.StatusBadGateway, "upstream_unavailable"
	default:
		status, code = http.StatusInternalServerError, "internal_error"
		h.logger.Error("upstream request failed", "request_id", st.event.RequestID, "error", err)
	}

	if result.StatusCode != 0 {
		// Response headers are already on the wire; never append a gateway
		// error document to a partial upstream response.
		st.event.ErrorCode = code
		return
	}
	h.fail(c, st, status, code, upstreamMessage(status), nil)
}

func (h *Handler) fail(c *gin.Context, st *requestState, status int, code, message string, rules []string) {
	if c.Writer.Written() {
		return
	}
	st.event.StatusCode = status
	st.event.ErrorCode = code
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(status, errorEnvelope{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: st.event.RequestID,
		Rules:     rules,
	}})
}

func (h *Handler) misconfigured(c *gin.Context, st *requestState, detail string) {
	h.logger.Error("gateway is misconfigured", "request_id", st.event.RequestID, "detail", detail)
	h.fail(c, st, http.StatusInternalServerError, "internal_error", "Internal gateway error", nil)
}

func (h *Handler) enqueueAudit(st *requestState, start time.Time) {
	latency := h.now().Sub(start)
	if latency < 0 {
		latency = 0
	}
	st.event.LatencyMS = int(latency.Milliseconds())
	if st.event.StatusCode == 0 {
		st.event.StatusCode = http.StatusInternalServerError
	}
	if st.event.DLPAction == "" {
		st.event.DLPAction = "none"
	}
	if h.deps.Audit != nil {
		h.deps.Audit.Enqueue(st.event)
	}
}

// enqueueAlerts emits one alert per rule that explicitly enables alerting for
// a Block or Audit hit. Mask-only hits do not alert.
func (h *Handler) enqueueAlerts(ctx context.Context, principal *auth.Principal, reqID string, hits []dlp.Hit) {
	if h.deps.AlertSink == nil {
		return
	}
	seen := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if !hit.AlertEnabled || (hit.Action != "block" && hit.Action != "audit") {
			continue
		}
		key := hit.RuleName
		if key == "" {
			key = hit.RuleID.String()
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		h.deps.AlertSink.Enqueue(ctx, alert.Alert{
			RuleName:  hit.RuleName,
			Employee:  principal.Employee.Name,
			Action:    hit.Action,
			RequestID: reqID,
			At:        h.now(),
		})
	}
}

type apiError struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	RequestID string   `json:"request_id"`
	Rules     []string `json:"rules,omitempty"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func authFailure(err error) (status int, responseCode, auditCode string) {
	switch {
	case errors.Is(err, auth.ErrMissingToken):
		return http.StatusUnauthorized, "invalid_token", "missing_token"
	case errors.Is(err, auth.ErrInvalidToken):
		return http.StatusUnauthorized, "invalid_token", "invalid_token"
	case errors.Is(err, auth.ErrTokenExpired):
		return http.StatusUnauthorized, "invalid_token", "token_expired"
	case errors.Is(err, auth.ErrTokenDisabled):
		return http.StatusUnauthorized, "invalid_token", "token_disabled"
	case errors.Is(err, auth.ErrIPNotAllowed):
		return http.StatusUnauthorized, "invalid_token", "ip_not_allowed"
	case errors.Is(err, auth.ErrEmployeeDisabled):
		return http.StatusUnauthorized, "invalid_token", "employee_disabled"
	default:
		return http.StatusServiceUnavailable, "auth_unavailable", "auth_unavailable"
	}
}

func authMessage(status int) string {
	if status == http.StatusServiceUnavailable {
		return "Authentication service unavailable"
	}
	return "Invalid or missing API token"
}

func upstreamMessage(status int) string {
	switch status {
	case http.StatusGatewayTimeout:
		return "Upstream provider timed out"
	case http.StatusBadGateway:
		return "Upstream provider is unavailable"
	default:
		return "Internal gateway error"
	}
}

func retryAfterHeader(retryAfter time.Duration) string {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func parseRequestMeta(body []byte) requestMeta {
	var meta requestMeta
	if len(body) == 0 {
		return meta
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return meta
	}
	if raw, ok := fields["model"]; ok {
		_ = json.Unmarshal(raw, &meta.Model)
	}
	for _, name := range []string{"max_completion_tokens", "max_tokens"} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value int64
		if json.Unmarshal(raw, &value) == nil && value > 0 {
			meta.MaxTokens = value
			break
		}
	}
	if raw, ok := fields["stream"]; ok {
		_ = json.Unmarshal(raw, &meta.Stream)
	}
	return meta
}

func classifyEndpoint(path, method string) quota.Endpoint {
	switch {
	case path == "/v1/models" && method == http.MethodGet:
		return quota.EndpointMetadata
	case path == "/v1/messages/count_tokens" && method == http.MethodPost:
		return quota.EndpointMetadata
	case path == "/v1/embeddings":
		return quota.EndpointEmbedding
	default:
		return quota.EndpointGeneration
	}
}

func uniqueRuleNames(hits []dlp.Hit) []string {
	seen := make(map[string]bool, len(hits))
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		if hit.RuleName == "" || seen[hit.RuleName] {
			continue
		}
		seen[hit.RuleName] = true
		names = append(names, hit.RuleName)
	}
	return names
}
