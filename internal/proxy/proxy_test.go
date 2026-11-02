package proxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/netguard"
	"github.com/company/api-key/internal/store"
)

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(bytes.Repeat([]byte{7}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func encryptedProvider(t *testing.T, c *crypto.Cipher, protocol, baseURL, key string) store.Provider {
	t.Helper()
	id := uuid.New()
	enc, err := c.Seal([]byte(key), "provider:"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	return store.Provider{
		ID:             id,
		Name:           "test",
		Protocol:       protocol,
		BaseURL:        baseURL,
		APIKeyEnc:      enc,
		Enabled:        true,
		TimeoutSeconds: 30,
	}
}

type observedRequest struct {
	method           string
	path             string
	query            string
	authorization    string
	xAPIKey          string
	anthropicVersion string
	cookie           string
	custom           string
	contentType      string
	model            string
	body             string
}

func TestProxyRewritesModelAndInjectsCredential(t *testing.T) {
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		observed <- observedRequest{
			method:        r.Method,
			path:          r.URL.Path,
			query:         r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"),
			xAPIKey:       r.Header.Get("x-api-key"),
			cookie:        r.Header.Get("Cookie"),
			custom:        r.Header.Get("X-Custom"),
			contentType:   r.Header.Get("Content-Type"),
			body:          body.String(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "upstream-req-1")
		w.Header().Set("X-Upstream", "kept")
		w.Header().Set("Set-Cookie", "upstream=secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "upstream-key")
	p := New(Config{Cipher: c, Client: upstream.Client(), CaptureLimit: 1024})

	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer sk-company-employee")
	header.Set("x-api-key", "sk-company-employee")
	header.Set("Cookie", "admin_session=secret")
	header.Set("X-Custom", "business")
	rec := httptest.NewRecorder()
	result, err := p.Do(context.Background(), rec, Request{
		Target:    Target{Provider: provider, Protocol: "openai_compatible", UpstreamModel: "real-model"},
		Path:      "/v1/chat/completions",
		RawQuery:  "trace=1",
		Body:      []byte(`{"model":"user-model","messages":[{"role":"user","content":"hi"}]}`),
		Header:    header,
		RequestID: "gateway-req-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := <-observed
	if got.method != http.MethodPost || got.path != "/v1/chat/completions" || got.query != "trace=1" {
		t.Fatalf("request line %+v", got)
	}
	if got.authorization != "Bearer upstream-key" {
		t.Fatalf("authorization %q", got.authorization)
	}
	if got.xAPIKey != "" {
		t.Fatalf("employee x-api-key leaked: %q", got.xAPIKey)
	}
	if got.cookie != "" {
		t.Fatalf("cookie leaked: %q", got.cookie)
	}
	if got.custom != "business" || got.contentType != "application/json" {
		t.Fatalf("headers %+v", got)
	}
	if !strings.Contains(got.body, `"model":"real-model"`) || !strings.Contains(got.body, `"messages"`) {
		t.Fatalf("body %s", got.body)
	}

	if result.StatusCode != http.StatusOK || result.Usage.TotalTokens != 7 || result.Usage.Estimated {
		t.Fatalf("result %+v", result)
	}
	if result.Usage.PromptTokens != 3 || result.Usage.CompletionTokens != 4 {
		t.Fatalf("usage %+v", result.Usage)
	}
	if result.UpstreamRequestID != "upstream-req-1" {
		t.Fatalf("upstream id %q", result.UpstreamRequestID)
	}
	if !strings.Contains(rec.Body.String(), `"chatcmpl-1"`) {
		t.Fatalf("client body %s", rec.Body.String())
	}
	if rec.Header().Get("X-Upstream") != "kept" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("response headers %v", rec.Header())
	}
}

func TestProxyAnthropicCredentialAndVersion(t *testing.T) {
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedRequest{
			authorization:    r.Header.Get("Authorization"),
			xAPIKey:          r.Header.Get("x-api-key"),
			anthropicVersion: r.Header.Get("anthropic-version"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"input_tokens":11,"output_tokens":5}}`))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "anthropic", upstream.URL, "anthropic-key")
	p := New(Config{Cipher: c, Client: upstream.Client()})

	header := http.Header{}
	header.Set("Authorization", "Bearer sk-company-employee")
	header.Set("anthropic-version", "2024-06-01")
	rec := httptest.NewRecorder()
	result, err := p.Do(context.Background(), rec, Request{
		Target: Target{Provider: provider, Protocol: "anthropic", UpstreamModel: "claude-upstream"},
		Path:   "/v1/messages",
		Body:   []byte(`{"model":"claude-requested","max_tokens":16,"messages":[]}`),
		Header: header,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := <-observed
	if got.xAPIKey != "anthropic-key" || got.authorization != "" {
		t.Fatalf("auth headers %+v", got)
	}
	if got.anthropicVersion != "2024-06-01" {
		t.Fatalf("client version not preserved: %q", got.anthropicVersion)
	}
	if result.Usage.PromptTokens != 11 || result.Usage.CompletionTokens != 5 || result.Usage.TotalTokens != 16 {
		t.Fatalf("usage %+v", result.Usage)
	}
}

func TestProxyDefaultAnthropicVersion(t *testing.T) {
	observed := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "anthropic", upstream.URL, "key")
	p := New(Config{Cipher: c, Client: upstream.Client()})
	rec := httptest.NewRecorder()
	_, err := p.Do(context.Background(), rec, Request{
		Target: Target{Provider: provider, Protocol: "anthropic"},
		Path:   "/v1/messages",
		Body:   []byte(`{"model":"x"}`),
		Header: http.Header{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if version := <-observed; version != "2023-06-01" {
		t.Fatalf("version %q", version)
	}
}

func TestProxyCaptureLimitDoesNotTruncateClient(t *testing.T) {
	payload := strings.Repeat("a", 200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
	p := New(Config{Cipher: c, Client: upstream.Client(), CaptureLimit: 17})
	rec := httptest.NewRecorder()
	result, err := p.Do(context.Background(), rec, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/completions",
		Body:   []byte(`{}`),
		Header: http.Header{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != payload {
		t.Fatalf("client got %d bytes", rec.Body.Len())
	}
	if len(result.ResponseBody) != 17 || string(result.ResponseBody) != payload[:17] {
		t.Fatalf("captured %d bytes", len(result.ResponseBody))
	}
	if !result.ResponseTruncated {
		t.Fatal("response truncation not recorded")
	}
	if !result.Usage.Estimated {
		t.Fatal("usage without upstream counts should be estimated")
	}
}

func TestProxyTransportErrors(t *testing.T) {
	c := testCipher(t)

	t.Run("unavailable", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		base := upstream.URL
		upstream.Close()
		provider := encryptedProvider(t, c, "openai_compatible", base, "key")
		p := New(Config{Cipher: c, Client: &http.Client{Timeout: time.Second}})
		_, err := p.Do(context.Background(), httptest.NewRecorder(), Request{
			Target: Target{Provider: provider, Protocol: "openai_compatible"},
			Path:   "/v1/chat/completions",
			Body:   []byte(`{}`),
			Header: http.Header{},
		})
		if !errors.Is(err, ErrUpstreamUnavailable) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer upstream.Close()
		provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
		p := New(Config{Cipher: c, Client: &http.Client{Timeout: 20 * time.Millisecond}})
		_, err := p.Do(context.Background(), httptest.NewRecorder(), Request{
			Target: Target{Provider: provider, Protocol: "openai_compatible"},
			Path:   "/v1/chat/completions",
			Body:   []byte(`{}`),
			Header: http.Header{},
		})
		if !errors.Is(err, ErrUpstreamTimeout) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("client disconnected", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer upstream.Close()
		provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
		p := New(Config{Cipher: c, Client: upstream.Client()})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := p.Do(ctx, httptest.NewRecorder(), Request{
			Target: Target{Provider: provider, Protocol: "openai_compatible"},
			Path:   "/v1/chat/completions",
			Body:   []byte(`{}`),
			Header: http.Header{},
		})
		if !errors.Is(err, ErrClientDisconnected) || !result.ClientDisconnected {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestProxyMissingCredential(t *testing.T) {
	p := New(Config{Client: &http.Client{}})
	_, err := p.Do(context.Background(), httptest.NewRecorder(), Request{
		Target: Target{Provider: store.Provider{ID: uuid.New(), BaseURL: "https://example.com"}},
		Path:   "/v1/models",
		Header: http.Header{},
	})
	if !errors.Is(err, ErrProviderConfig) {
		t.Fatalf("got %v", err)
	}
}

func TestProxyDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"redirected":true}`))
	}))
	defer target.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
	p := New(Config{
		Cipher: c,
		Client: netguard.NewClient(0, 120*time.Second, netguard.WithPrivateUpstreams()),
	})
	rec := httptest.NewRecorder()
	result, err := p.Do(context.Background(), rec, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/models",
		Header: http.Header{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusFound {
		t.Fatalf("status %d body %s", result.StatusCode, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "redirected") {
		t.Fatal("proxy followed redirect")
	}
}

func TestUpstreamURL(t *testing.T) {
	cases := []struct{ base, path, query, want string }{
		{"https://api.openai.com", "/v1/chat/completions", "", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/", "/v1/chat/completions", "a=1", "https://api.openai.com/v1/chat/completions?a=1"},
		{"https://host.example/openai/v1", "/v1/chat/completions", "", "https://host.example/openai/v1/chat/completions"},
		{"https://host.example/openai/v1/", "/v1/models", "", "https://host.example/openai/v1/models"},
	}
	for _, tc := range cases {
		got, err := upstreamURL(tc.base, tc.path, tc.query)
		if err != nil || got != tc.want {
			t.Errorf("%s + %s = %q,%v want %q", tc.base, tc.path, got, err, tc.want)
		}
	}
}
