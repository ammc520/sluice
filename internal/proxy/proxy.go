package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/netguard"
	"github.com/company/api-key/internal/store"
)

// DefaultCaptureLimit is how many response bytes are kept for audit.
const DefaultCaptureLimit = 256 * 1024

const defaultUpstreamTimeout = 300 * time.Second

// Target is the resolved upstream for one request.
type Target struct {
	Provider      store.Provider
	Protocol      string
	UpstreamModel string
}

// Usage is token accounting reported by an upstream response.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Estimated        bool
}

// Request is a gateway request to forward.
type Request struct {
	Target    Target
	Path      string
	RawQuery  string
	Body      []byte
	Header    http.Header
	RequestID string
	Stream    bool
}

// Result is the outcome of forwarding one request.
type Result struct {
	StatusCode         int
	Usage              Usage
	ResponseBody       []byte
	UpstreamRequestID  string
	FirstByteMS        int
	ClientDisconnected bool
}

// Config configures a Proxy.
type Config struct {
	Client       *http.Client
	Cipher       *crypto.Cipher
	CaptureLimit int
}

// Proxy forwards gateway requests to upstream providers.
type Proxy struct {
	client       *http.Client
	cipher       *crypto.Cipher
	captureLimit int
}

// New creates a proxy. A nil client uses production connection timeouts and
// does not follow redirects.
func New(cfg Config) *Proxy {
	client := cfg.Client
	if client == nil {
		client = defaultClient()
	}
	limit := cfg.CaptureLimit
	if limit <= 0 {
		limit = DefaultCaptureLimit
	}
	return &Proxy{client: client, cipher: cfg.Cipher, captureLimit: limit}
}

func defaultClient() *http.Client {
	return netguard.NewClient(0, 120*time.Second)
}

// Do forwards a non-streaming request upstream and writes the response to w.
// The caller owns translating the returned error into a client response.
func (p *Proxy) Do(ctx context.Context, w http.ResponseWriter, req Request) (Result, error) {
	return p.forward(ctx, w, req, false, nil)
}

func (p *Proxy) forward(ctx context.Context, w http.ResponseWriter, req Request, stream bool, flush func()) (Result, error) {
	apiKey, err := p.providerKey(req.Target.Provider)
	if err != nil {
		return Result{}, err
	}
	endpoint, err := upstreamURL(req.Target.Provider.BaseURL, req.Path, req.RawQuery)
	if err != nil {
		return Result{}, ErrProviderConfig
	}

	body := rewriteModel(req.Body, req.Target.UpstreamModel)
	var callCtx context.Context
	var cancel context.CancelFunc
	if stream {
		// Streaming uses an idle timeout instead of a total request deadline.
		callCtx, cancel = context.WithCancel(ctx)
	} else {
		callCtx, cancel = context.WithTimeout(ctx, providerTimeout(req.Target.Provider.TimeoutSeconds))
	}
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, ErrProviderConfig
	}
	// /v1/models is the only read-only supported endpoint.
	if req.Path == "/v1/models" && len(req.Body) == 0 {
		upstreamReq.Method = http.MethodGet
	}
	upstreamReq.Header = prepareHeaders(req.Header, req.Target, apiKey)
	if stream {
		// Never let transport gzip buffering delay SSE chunks.
		upstreamReq.Header.Set("Accept-Encoding", "identity")
	}
	if req.RequestID != "" {
		upstreamReq.Header.Set("X-Request-ID", req.RequestID)
	}

	start := time.Now()
	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		return p.transportError(ctx, callCtx, err)
	}
	defer resp.Body.Close()

	result := Result{
		StatusCode:        resp.StatusCode,
		UpstreamRequestID: upstreamRequestID(resp.Header),
		FirstByteMS:       int(time.Since(start).Milliseconds()),
	}

	copyResponseHeaders(w.Header(), resp.Header)
	if stream {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
	}
	w.WriteHeader(resp.StatusCode)
	if stream && flush != nil {
		flush()
	}

	captured := &captureWriter{w: w, limit: p.captureLimit}
	if stream {
		return p.copyStream(ctx, callCtx, cancel, w, captured, flush, resp, req.Target.Protocol, start, result)
	}

	if _, err := io.CopyBuffer(captured, resp.Body, make([]byte, 32*1024)); err != nil {
		result.ResponseBody = captured.bytes()
		if ctx.Err() != nil || callCtx.Err() == context.Canceled {
			result.ClientDisconnected = true
			return result, ErrClientDisconnected
		}
		return result, ErrUpstreamUnavailable
	}

	result.ResponseBody = captured.bytes()
	result.Usage = parseUsage(result.ResponseBody, req.Target.Protocol)
	return result, nil
}

func (p *Proxy) providerKey(provider store.Provider) (string, error) {
	if p.cipher == nil || len(provider.APIKeyEnc) == 0 {
		return "", ErrProviderConfig
	}
	plain, err := p.cipher.Open(provider.APIKeyEnc, "provider:"+provider.ID.String())
	if err != nil || len(plain) == 0 {
		return "", ErrProviderConfig
	}
	return string(plain), nil
}

func (p *Proxy) transportError(parent, call context.Context, err error) (Result, error) {
	if parent.Err() != nil {
		return Result{ClientDisconnected: true}, ErrClientDisconnected
	}
	if errors.Is(call.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return Result{}, ErrUpstreamTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return Result{}, ErrUpstreamTimeout
	}
	return Result{}, ErrUpstreamUnavailable
}

func providerTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultUpstreamTimeout
	}
	return time.Duration(seconds) * time.Second
}

func upstreamURL(base, requestPath, rawQuery string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ErrProviderConfig
	}
	basePath := strings.TrimRight(u.Path, "/")
	suffix := "/" + strings.TrimLeft(requestPath, "/")
	// A base URL may already end in /v1 (common for self-hosted gateways).
	if strings.HasSuffix(basePath, "/v1") && (suffix == "/v1" || strings.HasPrefix(suffix, "/v1/")) {
		suffix = strings.TrimPrefix(suffix, "/v1")
	}
	u.Path = basePath + suffix
	u.RawPath = ""
	u.RawQuery = rawQuery
	u.Fragment = ""
	return u.String(), nil
}

func rewriteModel(body []byte, model string) []byte {
	if model == "" || len(body) == 0 {
		return body
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	var existing string
	if raw, ok := fields["model"]; ok && json.Unmarshal(raw, &existing) == nil && existing == model {
		return body
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return body
	}
	fields["model"] = encoded
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return rewritten
}

func upstreamRequestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "openai-request-id", "anthropic-request-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

type captureWriter struct {
	w     io.Writer
	limit int
	buf   bytes.Buffer
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if remaining := c.limit - c.buf.Len(); remaining > 0 {
		if len(p) <= remaining {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:remaining])
		}
	}
	return c.w.Write(p)
}

func (c *captureWriter) bytes() []byte {
	return c.buf.Bytes()
}
