package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

type fakeAuth struct {
	principal *auth.Principal
	err       error
	calls     int
}

func (f *fakeAuth) Authenticate(*http.Request) (*auth.Principal, error) {
	f.calls++
	return f.principal, f.err
}

type fakeLimiter struct {
	allowed bool
	retry   time.Duration
	err     error
	calls   int
	key     string
	limit   int
}

func (f *fakeLimiter) Allow(_ context.Context, key string, limit int) (bool, time.Duration, error) {
	f.calls++
	f.key = key
	f.limit = limit
	return f.allowed, f.retry, f.err
}

type quotaReserveCall struct {
	token     *store.Token
	endpoint  quota.Endpoint
	maxTokens int64
}

type quotaSettleCall struct {
	reservation store.Reservation
	actual      int64
	ctxErr      error
}

type fakeQuota struct {
	reservation  store.Reservation
	reserveErr   error
	settleErr    error
	reserveCalls []quotaReserveCall
	settleCalls  []quotaSettleCall
	reserveCount int
	settleCount  int
}

func (f *fakeQuota) Reserve(_ context.Context, token *store.Token, endpoint quota.Endpoint, maxTokens int64) (store.Reservation, error) {
	f.reserveCount++
	f.reserveCalls = append(f.reserveCalls, quotaReserveCall{token: token, endpoint: endpoint, maxTokens: maxTokens})
	if f.reserveErr != nil {
		return store.Reservation{}, f.reserveErr
	}
	reservation := f.reservation
	if reservation.TokenID == uuid.Nil {
		reservation.TokenID = token.ID
	}
	return reservation, nil
}

func (f *fakeQuota) Settle(ctx context.Context, reservation store.Reservation, actual int64) error {
	f.settleCount++
	f.settleCalls = append(f.settleCalls, quotaSettleCall{reservation: reservation, actual: actual, ctxErr: ctx.Err()})
	return f.settleErr
}

type fakeDLP struct {
	result dlp.Result
	err    error
	inputs [][]byte
}

func (f *fakeDLP) Inspect(body []byte) (dlp.Result, error) {
	f.inputs = append(f.inputs, append([]byte(nil), body...))
	return f.result, f.err
}

type fakeResolver struct {
	target proxy.Target
	err    error
	path   string
	model  string
	calls  int
}

func (f *fakeResolver) Resolve(path, model string) (proxy.Target, error) {
	f.calls++
	f.path = path
	f.model = model
	return f.target, f.err
}

type fakeUpstream struct {
	result    proxy.Result
	err       error
	calls     int
	stream    bool
	request   proxy.Request
	body      []byte
	flushed   bool
	writeBody bool
}

func (f *fakeUpstream) Do(_ context.Context, w http.ResponseWriter, req proxy.Request) (proxy.Result, error) {
	return f.record(w, false, nil, req)
}

func (f *fakeUpstream) Stream(_ context.Context, w http.ResponseWriter, flush func(), req proxy.Request) (proxy.Result, error) {
	return f.record(w, true, flush, req)
}

func (f *fakeUpstream) record(w http.ResponseWriter, stream bool, flush func(), req proxy.Request) (proxy.Result, error) {
	f.calls++
	f.stream = stream
	f.request = req
	f.body = append([]byte(nil), req.Body...)
	if f.result.StatusCode != 0 && f.writeBody {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.result.StatusCode)
		if stream && flush != nil {
			flush()
			f.flushed = true
		}
		_, _ = w.Write(f.result.ResponseBody)
	}
	return f.result, f.err
}

type fakeAudit struct {
	events []audit.Event
	calls  int
}

func (f *fakeAudit) Enqueue(e audit.Event) bool {
	f.calls++
	f.events = append(f.events, e)
	return true
}

func (f *fakeAudit) last() audit.Event {
	if len(f.events) == 0 {
		return audit.Event{}
	}
	return f.events[len(f.events)-1]
}

type fakeAlerts struct {
	alerts []alert.Alert
}

func (f *fakeAlerts) Enqueue(_ context.Context, a alert.Alert) bool {
	f.alerts = append(f.alerts, a)
	return true
}

type handlerFixture struct {
	handler  *Handler
	auth     *fakeAuth
	limiter  *fakeLimiter
	quota    *fakeQuota
	dlp      *fakeDLP
	resolver *fakeResolver
	upstream *fakeUpstream
	audit    *fakeAudit
	alerts   *fakeAlerts
	now      time.Time
	tokenID  uuid.UUID
	employee store.Employee
	provider store.Provider
}

func newHandlerFixture(t *testing.T) (*Handler, *handlerFixture) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	department := int64(7)
	tokenID := uuid.New()
	employee := store.Employee{
		ID:           uuid.New(),
		EmployeeNo:   "E001",
		Name:         "Alice",
		Email:        "alice@example.com",
		DepartmentID: &department,
		Status:       1,
	}
	token := store.Token{
		ID:           tokenID,
		EmployeeID:   employee.ID,
		QuotaType:    "tokens",
		QuotaPeriod:  "daily",
		RateLimitRPM: 60,
		Status:       1,
	}
	provider := store.Provider{ID: uuid.New(), Name: "openai", Protocol: "openai_compatible", Enabled: true}
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))

	f := &handlerFixture{
		auth: &fakeAuth{principal: &auth.Principal{Token: token, Employee: employee}},
		limiter: &fakeLimiter{
			allowed: true,
		},
		quota: &fakeQuota{reservation: store.Reservation{TokenID: tokenID, Reserved: 4096}},
		dlp:   &fakeDLP{result: dlp.Result{Action: "none"}},
		resolver: &fakeResolver{target: proxy.Target{
			Provider:      provider,
			Protocol:      "openai_compatible",
			UpstreamModel: "gpt-4o",
		}},
		upstream: &fakeUpstream{
			result: proxy.Result{
				StatusCode:   200,
				Usage:        proxy.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
				ResponseBody: []byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`),
			},
			writeBody: true,
		},
		audit:    &fakeAudit{},
		alerts:   &fakeAlerts{},
		now:      now,
		tokenID:  tokenID,
		employee: employee,
		provider: provider,
	}

	f.handler = New(HandlerDeps{
		Auth:      f.auth,
		Limiter:   f.limiter,
		Quota:     f.quota,
		DLP:       f.dlp,
		Resolver:  f.resolver,
		Upstream:  f.upstream,
		Audit:     f.audit,
		AlertSink: f.alerts,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:     func() time.Time { return f.now },
	})
	return f.handler, f
}

func (f *handlerFixture) run(t *testing.T, method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer sk-company-test")
	if mutate != nil {
		mutate(c.Request)
	}
	f.handler.Handle(c)
	return rr
}

type testErrorEnvelope struct {
	Error struct {
		Code      string   `json:"code"`
		Message   string   `json:"message"`
		RequestID string   `json:"request_id"`
		Rules     []string `json:"rules"`
	} `json:"error"`
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) testErrorEnvelope {
	t.Helper()
	var env testErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v body=%q", err, rr.Body.String())
	}
	return env
}

func TestPipelineBlockReturns403AndDoesNotCallUpstream(t *testing.T) {
	_, f := newHandlerFixture(t)
	ruleID := uuid.New()
	f.dlp.result = dlp.Result{
		Action: "block",
		Hits: []dlp.Hit{{
			RuleID: ruleID, RuleName: "云厂商 AK/SK", Action: "block", AlertEnabled: true,
			Start: 10, End: 30,
		}},
	}
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"AKIAIOSFODNN7EXAMPLE"}]}`
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", body, nil)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	env := decodeError(t, rr)
	if env.Error.Code != "dlp_blocked" || env.Error.RequestID == "" ||
		len(env.Error.Rules) != 1 || env.Error.Rules[0] != "云厂商 AK/SK" {
		t.Fatalf("error body %+v", env)
	}
	if strings.Contains(rr.Body.String(), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("blocked response echoed sensitive content")
	}
	if f.upstream.calls != 0 {
		t.Fatal("upstream called for blocked request")
	}
	if f.quota.reserveCount != 0 {
		t.Fatal("quota reserved for blocked request")
	}
	if len(f.dlp.inputs) != 1 || string(f.dlp.inputs[0]) != body {
		t.Fatalf("DLP inputs %q", f.dlp.inputs)
	}
	if f.audit.calls != 1 {
		t.Fatalf("audit calls %d", f.audit.calls)
	}
	event := f.audit.last()
	if event.StatusCode != http.StatusForbidden || event.ErrorCode != "dlp_blocked" ||
		event.DLPAction != "block" || len(event.DLPHits) != 1 ||
		string(event.Prompt) != body || event.TokenID == nil || *event.TokenID != f.tokenID {
		t.Fatalf("audit event %+v", event)
	}
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("alerts %+v", f.alerts.alerts)
	}
	got := f.alerts.alerts[0]
	if got.RuleName != "云厂商 AK/SK" || got.Action != "block" ||
		got.Employee != "Alice" || got.RequestID != event.RequestID || !got.At.Equal(f.now) {
		t.Fatalf("alert %+v", got)
	}
}

func TestPipelineMasksBeforeForwarding(t *testing.T) {
	_, f := newHandlerFixture(t)
	original := `{"model":"gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"call 13800138000"}]}`
	masked := `{"model":"gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"call ***"}]}`
	f.dlp.result = dlp.Result{
		Action: "mask",
		Body:   []byte(masked),
		Hits: []dlp.Hit{{
			RuleID: uuid.New(), RuleName: "手机号", Action: "mask", AlertEnabled: true,
		}},
	}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", original, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if f.upstream.calls != 1 || string(f.upstream.body) != masked {
		t.Fatalf("upstream body %q calls %d", f.upstream.body, f.upstream.calls)
	}
	if strings.Contains(string(f.upstream.body), "13800138000") {
		t.Fatal("phone number forwarded")
	}
	if event := f.audit.last(); event.DLPAction != "mask" || string(event.Prompt) != original {
		t.Fatalf("audit event %+v", event)
	}
	if f.quota.reserveCount != 1 || f.quota.reserveCalls[0].endpoint != quota.EndpointGeneration ||
		f.quota.reserveCalls[0].maxTokens != 50 {
		t.Fatalf("quota reserve %+v", f.quota.reserveCalls)
	}
	if f.quota.settleCount != 1 || f.quota.settleCalls[0].actual != 7 {
		t.Fatalf("quota settle %+v", f.quota.settleCalls)
	}
	if len(f.alerts.alerts) != 0 {
		t.Fatalf("mask-only hits must not alert: %+v", f.alerts.alerts)
	}
}

func TestAuditHitsAlertWithoutSensitiveContent(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.dlp.result = dlp.Result{
		Action: "audit",
		Hits: []dlp.Hit{
			{RuleID: uuid.New(), RuleName: "内网 IP", Action: "audit", AlertEnabled: true},
			{RuleID: uuid.New(), RuleName: "内网 IP", Action: "audit", AlertEnabled: true},
			{RuleID: uuid.New(), RuleName: "邮箱地址", Action: "audit", AlertEnabled: false},
		},
	}
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"10.0.0.1 a@b.com"}]}`
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", body, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if string(f.upstream.body) != body {
		t.Fatalf("audit-only hit rewrote body: %q", f.upstream.body)
	}
	if len(f.alerts.alerts) != 1 || f.alerts.alerts[0].RuleName != "内网 IP" {
		t.Fatalf("alerts %+v", f.alerts.alerts)
	}
	if event := f.audit.last(); event.DLPAction != "audit" || len(event.DLPHits) != 3 {
		t.Fatalf("audit event %+v", event)
	}
}

func TestQuotaExceededReturns402(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.quota.reserveErr = store.ErrQuotaExceeded
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if env := decodeError(t, rr); env.Error.Code != "quota_exceeded" {
		t.Fatalf("error %+v", env)
	}
	if f.upstream.calls != 0 {
		t.Fatal("upstream called after quota rejection")
	}
	if event := f.audit.last(); event.StatusCode != http.StatusPaymentRequired || event.ErrorCode != "quota_exceeded" {
		t.Fatalf("audit event %+v", event)
	}
}

func TestQuotaInfrastructureFailureReturns503(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.quota.reserveErr = errors.New("database unavailable")
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if env := decodeError(t, rr); env.Error.Code != "quota_unavailable" {
		t.Fatalf("error %+v", env)
	}
	if f.upstream.calls != 0 {
		t.Fatal("upstream called while quota was unavailable")
	}
}

func TestAuthFailures(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		code     string
		auditErr string
	}{
		{"expired", auth.ErrTokenExpired, http.StatusUnauthorized, "invalid_token", "token_expired"},
		{"disabled", auth.ErrTokenDisabled, http.StatusUnauthorized, "invalid_token", "token_disabled"},
		{"invalid", auth.ErrInvalidToken, http.StatusUnauthorized, "invalid_token", "invalid_token"},
		{"missing", auth.ErrMissingToken, http.StatusUnauthorized, "invalid_token", "missing_token"},
		{"ip", auth.ErrIPNotAllowed, http.StatusUnauthorized, "invalid_token", "ip_not_allowed"},
		{"employee", auth.ErrEmployeeDisabled, http.StatusUnauthorized, "invalid_token", "employee_disabled"},
		{"infra", errors.New("postgres down"), http.StatusServiceUnavailable, "auth_unavailable", "auth_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := newHandlerFixture(t)
			f.auth.err = tc.err
			rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
			if rr.Code != tc.status {
				t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
			}
			env := decodeError(t, rr)
			if env.Error.Code != tc.code {
				t.Fatalf("error code %q", env.Error.Code)
			}
			if tc.code == "invalid_token" && strings.Contains(strings.ToLower(env.Error.Message), "expired") {
				t.Fatal("auth error leaked validation detail")
			}
			if f.upstream.calls != 0 || f.dlp.inputs != nil {
				t.Fatal("downstream stages ran after auth failure")
			}
			if event := f.audit.last(); event.StatusCode != tc.status || event.ErrorCode != tc.auditErr {
				t.Fatalf("audit event %+v", event)
			}
		})
	}
}

func TestRateLimitReturns429WithRetryAfter(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.limiter.allowed = false
	f.limiter.retry = 1500 * time.Millisecond
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After %q", got)
	}
	if env := decodeError(t, rr); env.Error.Code != "rate_limited" {
		t.Fatalf("error %+v", env)
	}
	if f.limiter.calls != 1 || f.limiter.key != f.tokenID.String() || f.limiter.limit != 60 {
		t.Fatalf("limiter call %+v", f.limiter)
	}
	if f.upstream.calls != 0 {
		t.Fatal("upstream called after rate limit")
	}
	if event := f.audit.last(); event.StatusCode != http.StatusTooManyRequests ||
		event.TokenID == nil || *event.TokenID != f.tokenID {
		t.Fatalf("audit event %+v", event)
	}
}

func TestRateLimiterFailureFailsOpen(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.limiter.err = errors.New("redis and memory unavailable")
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if f.upstream.calls != 1 {
		t.Fatal("upstream not called after limiter degradation")
	}
}

func TestUnsupportedEndpointReturns501(t *testing.T) {
	_, f := newHandlerFixture(t)
	rr := f.run(t, http.MethodPost, "/v1/audio/speech", `{"model":"tts"}`, nil)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status %d", rr.Code)
	}
	if env := decodeError(t, rr); env.Error.Code != "unsupported_endpoint" {
		t.Fatalf("error %+v", env)
	}
	if f.resolver.calls != 0 || f.upstream.calls != 0 || f.dlp.inputs != nil {
		t.Fatal("unsupported path reached later stages")
	}
}

func TestResolverFailureReturns400(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.resolver.err = proxy.ErrNoRoute
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"unknown"}`, nil)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
	if env := decodeError(t, rr); env.Error.Code != "no_route" {
		t.Fatalf("error %+v", env)
	}
	if f.resolver.path != "/v1/chat/completions" || f.resolver.model != "unknown" {
		t.Fatalf("resolver call path=%q model=%q", f.resolver.path, f.resolver.model)
	}
	if f.upstream.calls != 0 || f.dlp.inputs != nil {
		t.Fatal("resolver failure reached later stages")
	}
}

func TestDLPUnsupportedContentReturns415(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.dlp.err = dlp.ErrUnsupportedContent
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d", rr.Code)
	}
	if env := decodeError(t, rr); env.Error.Code != "unsupported_media" {
		t.Fatalf("error %+v", env)
	}
	if f.upstream.calls != 0 || f.quota.reserveCount != 0 {
		t.Fatal("unsupported content reached upstream or quota")
	}
}

func TestDLPUninspectableJSONReturns400(t *testing.T) {
	for _, inspectionErr := range []error{dlp.ErrDuplicateKeys, dlp.ErrTooManyMatches} {
		_, f := newHandlerFixture(t)
		f.dlp.err = inspectionErr
		rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%v: status %d", inspectionErr, rr.Code)
		}
		if env := decodeError(t, rr); env.Error.Code != "invalid_request" {
			t.Fatalf("%v: error %+v", inspectionErr, env)
		}
		if f.upstream.calls != 0 || f.quota.reserveCount != 0 {
			t.Fatalf("%v: request reached later stages", inspectionErr)
		}
	}
}

func TestMaskedModelIsNotRewrittenToString(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.dlp.result = dlp.Result{
		Action: "mask",
		Body:   []byte(`{"model":"***","messages":[]}`),
		Hits:   []dlp.Hit{{RuleName: "phone", Action: "mask", AlertEnabled: true}},
	}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"13800138000","messages":[]}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if f.upstream.request.Target.UpstreamModel != "***" {
		t.Fatalf("upstream model %q", f.upstream.request.Target.UpstreamModel)
	}
	if string(f.upstream.body) != `{"model":"***","messages":[]}` {
		t.Fatalf("upstream body %q", f.upstream.body)
	}
}

func TestBodyTooLargeReturns413(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.handler.maxBody = 32
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"`+strings.Repeat("a", 100)+`"}]}`, nil)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if env := decodeError(t, rr); env.Error.Code != "payload_too_large" {
		t.Fatalf("error %+v", env)
	}
	if f.dlp.inputs != nil || f.upstream.calls != 0 {
		t.Fatal("oversized body reached later stages")
	}
}

func TestRequestIDHeader(t *testing.T) {
	t.Run("server id differs from client correlation id", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
			r.Header.Set(RequestIDHeader, "client-req-123")
		})
		got := rr.Header().Get(RequestIDHeader)
		if len(got) != 36 || got == "client-req-123" {
			t.Fatalf("response request id %q", got)
		}
		if client := rr.Header().Get(ClientRequestIDHeader); client != "client-req-123" {
			t.Fatalf("client request id %q", client)
		}
		if event := f.audit.last(); event.RequestID != got {
			t.Fatalf("audit request id %q want %q", event.RequestID, got)
		}
		if f.upstream.request.RequestID != got {
			t.Fatalf("upstream request id %q want %q", f.upstream.request.RequestID, got)
		}
	})

	t.Run("client cannot reuse an audit id", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		for i := 0; i < 2; i++ {
			f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
				r.Header.Set(RequestIDHeader, "same-id")
			})
		}
		if len(f.audit.events) != 2 || f.audit.events[0].RequestID == f.audit.events[1].RequestID {
			t.Fatalf("audit request ids %+v", f.audit.events)
		}
	})

	t.Run("unsafe client value is not echoed", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
			r.Header.Set(RequestIDHeader, "bad id with spaces")
		})
		got := rr.Header().Get(RequestIDHeader)
		if got == "bad id with spaces" || len(got) != 36 {
			t.Fatalf("generated request id %q", got)
		}
		if client := rr.Header().Get(ClientRequestIDHeader); client != "" {
			t.Fatalf("unsafe client id echoed as %q", client)
		}
		if event := f.audit.last(); event.RequestID != got {
			t.Fatalf("audit request id %q want %q", event.RequestID, got)
		}
	})
}

func TestAuditClientIPUsesTrustedProxyRules(t *testing.T) {
	t.Run("untrusted peer cannot spoof forwarded header", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
			r.RemoteAddr = "198.51.100.9:1234"
			r.Header.Set("X-Forwarded-For", "203.0.113.7")
		})
		if event := f.audit.last(); event.ClientIP != "198.51.100.9" {
			t.Fatalf("client ip %q", event.ClientIP)
		}
	})

	t.Run("trusted proxy forwards the original client", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		f.handler.deps.TrustedProxies = []string{"192.0.2.0/24"}
		f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
			r.RemoteAddr = "192.0.2.10:1234"
			r.Header.Set("X-Forwarded-For", "203.0.113.7, 192.0.2.11")
		})
		if event := f.audit.last(); event.ClientIP != "203.0.113.7" {
			t.Fatalf("client ip %q", event.ClientIP)
		}
	})
}

func TestMetadataEndpointsSkipQuota(t *testing.T) {
	t.Run("models", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		rr := f.run(t, http.MethodGet, "/v1/models", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
		if f.quota.reserveCount != 0 || f.quota.settleCount != 0 {
			t.Fatalf("metadata touched quota: reserve=%d settle=%d", f.quota.reserveCount, f.quota.settleCount)
		}
		if f.upstream.calls != 1 || f.upstream.stream {
			t.Fatalf("upstream calls=%d stream=%v", f.upstream.calls, f.upstream.stream)
		}
		if f.resolver.model != "" {
			t.Fatalf("resolver model %q", f.resolver.model)
		}
		if event := f.audit.last(); event.StatusCode != http.StatusOK || event.ProviderID == nil ||
			*event.ProviderID != f.provider.ID {
			t.Fatalf("audit event %+v", event)
		}
	})

	t.Run("count tokens", func(t *testing.T) {
		_, f := newHandlerFixture(t)
		rr := f.run(t, http.MethodPost, "/v1/messages/count_tokens", `{"model":"claude","messages":[]}`, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
		if f.quota.reserveCount != 0 || f.quota.settleCount != 0 {
			t.Fatalf("count_tokens touched quota: %+v %+v", f.quota.reserveCalls, f.quota.settleCalls)
		}
	})
}

func TestEmbeddingUsesEmbeddingQuota(t *testing.T) {
	_, f := newHandlerFixture(t)
	rr := f.run(t, http.MethodPost, "/v1/embeddings", `{"model":"embed","input":"hello"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if f.quota.reserveCount != 1 || f.quota.reserveCalls[0].endpoint != quota.EndpointEmbedding {
		t.Fatalf("quota reserve %+v", f.quota.reserveCalls)
	}
	if f.quota.settleCount != 1 || f.quota.settleCalls[0].actual != 7 {
		t.Fatalf("quota settle %+v", f.quota.settleCalls)
	}
}

func TestRequestQuotaSettlesOne(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.auth.principal.Token.QuotaType = "requests"
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":100}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if f.quota.settleCount != 1 || f.quota.settleCalls[0].actual != 1 {
		t.Fatalf("quota settle %+v", f.quota.settleCalls)
	}
}

func TestMissingUsageSettlesReservation(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result.Usage = proxy.Usage{Estimated: true}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if f.quota.settleCount != 1 || f.quota.settleCalls[0].actual != 4096 {
		t.Fatalf("quota settle %+v", f.quota.settleCalls)
	}
	event := f.audit.last()
	if !event.UsageEstimated || event.TotalTokens != 0 {
		t.Fatalf("audit event %+v", event)
	}
}

func TestStreamUsesFlusherAndSettles(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result = proxy.Result{
		StatusCode:   200,
		Usage:        proxy.Usage{PromptTokens: 2, CompletionTokens: 5, TotalTokens: 7},
		ResponseBody: []byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":5,\"total_tokens\":7}}\n\ndata: [DONE]\n\n"),
	}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","stream":true}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !f.upstream.stream || !f.upstream.request.Stream || !f.upstream.flushed {
		t.Fatalf("stream=%v request.Stream=%v flushed=%v", f.upstream.stream, f.upstream.request.Stream, f.upstream.flushed)
	}
	if f.quota.settleCount != 1 || f.quota.settleCalls[0].actual != 7 {
		t.Fatalf("quota settle %+v", f.quota.settleCalls)
	}
}

func TestUpstreamErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", proxy.ErrUpstreamTimeout, http.StatusGatewayTimeout, "upstream_timeout"},
		{"unavailable", proxy.ErrUpstreamUnavailable, http.StatusBadGateway, "upstream_unavailable"},
		{"provider config", proxy.ErrProviderConfig, http.StatusInternalServerError, "internal_error"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := newHandlerFixture(t)
			f.upstream.result = proxy.Result{}
			f.upstream.err = tc.err
			rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
			if rr.Code != tc.status {
				t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
			}
			if env := decodeError(t, rr); env.Error.Code != tc.code {
				t.Fatalf("error %+v", env)
			}
			if event := f.audit.last(); event.StatusCode != tc.status || event.ErrorCode != tc.code {
				t.Fatalf("audit event %+v", event)
			}
			if f.quota.settleCount != 1 {
				t.Fatalf("failed upstream call not settled: %+v", f.quota.settleCalls)
			}
		})
	}
}

func TestUpstreamErrorAfterHeadersDoesNotAppendErrorBody(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result = proxy.Result{
		StatusCode:   200,
		ResponseBody: []byte(`{"partial":true}`),
	}
	f.upstream.err = proxy.ErrUpstreamUnavailable
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if rr.Body.String() != `{"partial":true}` {
		t.Fatalf("body %q", rr.Body.String())
	}
	event := f.audit.last()
	if event.StatusCode != http.StatusOK || event.ErrorCode != "upstream_unavailable" {
		t.Fatalf("audit event %+v", event)
	}
}

func TestClientDisconnectIsRecorded(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result = proxy.Result{ClientDisconnected: true}
	f.upstream.err = proxy.ErrClientDisconnected
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)

	if rr.Body.Len() != 0 {
		t.Fatalf("wrote body after client disconnect: %q", rr.Body.String())
	}
	event := f.audit.last()
	if !event.ClientDisconnected || event.StatusCode != 499 {
		t.Fatalf("audit event %+v", event)
	}
	if f.quota.settleCount != 1 {
		t.Fatal("disconnect did not settle quota")
	}
}

func TestUpstreamStatusPassthrough(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result = proxy.Result{StatusCode: 429, ResponseBody: []byte(`{"error":{"message":"slow down"}}`)}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d", rr.Code)
	}
	if rr.Body.String() != `{"error":{"message":"slow down"}}` {
		t.Fatalf("body %q", rr.Body.String())
	}
	if event := f.audit.last(); event.StatusCode != http.StatusTooManyRequests || event.ErrorCode != "upstream_error" {
		t.Fatalf("audit event %+v", event)
	}
}

func TestMalformedJSONIsForwardedAsText(t *testing.T) {
	_, f := newHandlerFixture(t)
	body := "not json at all"
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if f.resolver.model != "" {
		t.Fatalf("model %q", f.resolver.model)
	}
	if string(f.upstream.body) != body || string(f.dlp.inputs[0]) != body {
		t.Fatalf("body was altered: upstream=%q dlp=%q", f.upstream.body, f.dlp.inputs[0])
	}
}

func TestOversizedModelIsRejected(t *testing.T) {
	_, f := newHandlerFixture(t)
	body := `{"model":"` + strings.Repeat("m", 129) + `"}`
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", body, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if f.resolver.calls != 0 || f.upstream.calls != 0 {
		t.Fatal("oversized model reached resolver or upstream")
	}
}

func TestEmptyDLPActionIsNormalized(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.dlp.result = dlp.Result{}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if event := f.audit.last(); event.DLPAction != "none" {
		t.Fatalf("DLP action %q", event.DLPAction)
	}
}

func TestAuditEventIncludesUsageAndTiming(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.upstream.result = proxy.Result{
		StatusCode:        200,
		Usage:             proxy.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		ResponseBody:      []byte(`{"id":"x"}`),
		UpstreamRequestID: "upstream-1",
		FirstByteMS:       12,
	}
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	event := f.audit.last()
	if event.PromptTokens != 3 || event.CompletionTokens != 4 || event.TotalTokens != 7 ||
		event.UsageEstimated || event.FirstByteMS != 12 || event.UpstreamRequestID != "upstream-1" {
		t.Fatalf("audit event %+v", event)
	}
	if string(event.Response) != `{"id":"x"}` {
		t.Fatalf("response %q", event.Response)
	}
	if event.DepartmentID == nil || *event.DepartmentID != *f.employee.DepartmentID {
		t.Fatalf("department %+v", event.DepartmentID)
	}
	if event.Model != "gpt-4o" || event.ProviderID == nil || *event.ProviderID != f.provider.ID {
		t.Fatalf("routing audit fields %+v", event)
	}
}

func TestErrorResponseEnvelopeHasRequestID(t *testing.T) {
	_, f := newHandlerFixture(t)
	f.auth.err = auth.ErrInvalidToken
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{}`, func(r *http.Request) {
		r.Header.Set(RequestIDHeader, "fixed-req-id")
	})
	env := decodeError(t, rr)
	if env.Error.RequestID == "" || env.Error.RequestID == "fixed-req-id" {
		t.Fatalf("request id %q", env.Error.RequestID)
	}
	if env.Error.RequestID != rr.Header().Get(RequestIDHeader) {
		t.Fatalf("response request id %q error request id %q",
			rr.Header().Get(RequestIDHeader), env.Error.RequestID)
	}
	if rr.Header().Get(ClientRequestIDHeader) != "fixed-req-id" {
		t.Fatalf("client request id %q", rr.Header().Get(ClientRequestIDHeader))
	}
	if rr.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", rr.Header().Get("Content-Type"))
	}
}

func TestAuditEnqueueAlwaysRuns(t *testing.T) {
	h, f := newHandlerFixture(t)
	h.deps.Auth = nil
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{}`, nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rr.Code)
	}
	if f.audit.calls != 1 {
		t.Fatalf("audit calls %d", f.audit.calls)
	}
}

func TestHandlerDoesNotMutateClientHeaders(t *testing.T) {
	_, f := newHandlerFixture(t)
	original := http.Header{}
	original.Set("Authorization", "Bearer sk-company-test")
	rr := f.run(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, func(r *http.Request) {
		r.Header = original
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if got := f.upstream.request.Header.Get("Authorization"); got != "Bearer sk-company-test" {
		t.Fatalf("upstream header %q", got)
	}
	if got := original.Get("Authorization"); got != "Bearer sk-company-test" {
		t.Fatalf("handler mutated client header %q", got)
	}
}
