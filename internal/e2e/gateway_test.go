package e2e_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/dlp"
	"github.com/company/sluice/internal/store"
	"github.com/company/sluice/internal/store/postgres"
)

func TestNonStreamingMaskRewritesUpstreamAndKeepsEncryptedAudit(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})

	resp, body := env.post("/v1/chat/completions", chatBody("call me at 13800138000", 100, false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}

	upstream := env.nextRequest(t)
	if bytes.Contains(upstream.Body, []byte("13800138000")) {
		t.Fatalf("upstream received the unmasked phone number: %s", upstream.Body)
	}
	if !bytes.Contains(upstream.Body, []byte("***")) {
		t.Fatalf("upstream body was not masked: %s", upstream.Body)
	}
	if got := upstream.Header.Get("Authorization"); got == "" {
		t.Fatal("upstream did not receive a provider credential")
	}

	record := env.waitAudit(t, func(r *store.AuditRecord) bool {
		return r.StatusCode == http.StatusOK && r.DLPAction == "mask"
	})
	if record.TotalTokens != 11 {
		t.Fatalf("total tokens %d, want 11", record.TotalTokens)
	}
	if len(record.PromptEnc) == 0 || bytes.Contains(record.PromptEnc, []byte("13800138000")) {
		t.Fatal("audit prompt was not encrypted")
	}
	cipher, err := newTestCipher()
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := cipher.Open(record.PromptEnc, record.RequestID+":prompt")
	if err != nil {
		t.Fatalf("decrypt prompt: %v", err)
	}
	if !bytes.Contains(prompt, []byte("13800138000")) {
		t.Fatalf("audit did not retain the original prompt: %s", prompt)
	}
	response, err := cipher.Open(record.ResponseEnc, record.RequestID+":response")
	if err != nil {
		t.Fatalf("decrypt response: %v", err)
	}
	if !bytes.Contains(response, []byte("chatcmpl-ok")) {
		t.Fatalf("audit response %s", response)
	}

	metricsResp, metricsBody := env.do(context.Background(), http.MethodGet, "/metrics", nil, "")
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status %d", metricsResp.StatusCode)
	}
	if !strings.Contains(string(metricsBody), `gateway_requests_total{dlp_action="mask",status="200"}`) {
		t.Fatalf("request metric missing:\n%s", metricsBody)
	}
	if strings.Contains(string(metricsBody), env.token) {
		t.Fatal("metrics leaked a company token")
	}
}

func TestStreamingPassesChunksAndRecordsUsage(t *testing.T) {
	env := newGatewayEnv(t, envSetup{upstream: openAIStream(20)})

	resp, body := env.post("/v1/chat/completions", chatBody("stream please", 100, true))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type %q", got)
	}
	for _, want := range []string{"chunk-0", "chunk-19", "[DONE]"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("stream missing %q: %s", want, body)
		}
	}

	record := env.waitAudit(t, func(r *store.AuditRecord) bool {
		return r.StatusCode == http.StatusOK && r.TotalTokens == 11
	})
	if record.UsageEstimated {
		t.Fatal("streaming usage was marked estimated")
	}
	env.nextRequest(t)

	var used int64
	if err := env.pool.QueryRow(context.Background(),
		`SELECT quota_used FROM tokens WHERE id = $1`, env.tokenID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 11 {
		t.Fatalf("quota used %d, want 11", used)
	}
}

func TestDLPBlockPreventsUpstreamCall(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})

	resp, body := env.post("/v1/chat/completions", chatBody("key AKIAIOSFODNN7EXAMPLE", 100, false))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"code":"dlp_blocked"`) {
		t.Fatalf("body %s", body)
	}
	env.waitNoRequest(t)
	record := env.waitAudit(t, func(r *store.AuditRecord) bool {
		return r.StatusCode == http.StatusForbidden
	})
	if record.DLPAction != "block" {
		t.Fatalf("dlp action %q", record.DLPAction)
	}
	if !bytes.Contains(record.DLPHits, []byte(dlp.BuiltinCloudKeyName)) {
		t.Fatalf("audit hits %s", record.DLPHits)
	}
}

func TestDLPAuditActionForwardsOriginalContent(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})

	resp, body := env.post("/v1/chat/completions", chatBody("contact alice@company.example", 100, false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	upstream := env.nextRequest(t)
	if !bytes.Contains(upstream.Body, []byte("alice@company.example")) {
		t.Fatalf("audit action must forward original content: %s", upstream.Body)
	}
	record := env.waitAudit(t, func(r *store.AuditRecord) bool {
		return r.DLPAction == "audit" && r.StatusCode == http.StatusOK
	})
	if record.DLPAction != "audit" {
		t.Fatalf("dlp action %q", record.DLPAction)
	}
}

func TestQuotaExhaustionReturns402(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE tokens SET quota_total = 10 WHERE id = $1`, env.tokenID); err != nil {
		t.Fatal(err)
	}

	resp, body := env.post("/v1/chat/completions", chatBody("hello", 100, false))
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"code":"quota_exceeded"`) {
		t.Fatalf("body %s", body)
	}
	env.waitNoRequest(t)
}

func TestRateLimitReturns429(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE tokens SET rate_limit_rpm = 1 WHERE id = $1`, env.tokenID); err != nil {
		t.Fatal(err)
	}

	first, body := env.post("/v1/chat/completions", chatBody("first", 10, false))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status %d body %s", first.StatusCode, body)
	}
	second, body := env.post("/v1/chat/completions", chatBody("second", 10, false))
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second status %d body %s", second.StatusCode, body)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}

func TestRequestValidationStatuses(t *testing.T) {
	env := newGatewayEnv(t, envSetup{bodyLimit: 1024})

	t.Run("missing token", func(t *testing.T) {
		resp, body := env.do(context.Background(), http.MethodPost, "/v1/chat/completions",
			chatBody("hello", 10, false), "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("unsupported endpoint", func(t *testing.T) {
		resp, body := env.post("/v1/not-a-real-endpoint", chatBody("hello", 10, false))
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("binary content", func(t *testing.T) {
		resp, body := env.post("/v1/chat/completions", []byte{0xff, 0xfe, 0xfd})
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("image content block", func(t *testing.T) {
		resp, body := env.post("/v1/chat/completions",
			[]byte(`{"model":"test-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`))
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		oversized := append(chatBody("hello", 10, false), bytes.Repeat([]byte(" "), 4096)...)
		resp, body := env.post("/v1/chat/completions", oversized)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	env.waitNoRequest(t)
}

func TestDLPAlertSendsOneEmail(t *testing.T) {
	env := newGatewayEnv(t, envSetup{
		seed: func(ctx context.Context, pg *postgres.Store, _ *gatewayEnv) {
			if err := pg.DLPRules.Create(ctx, &store.DLPRule{
				Name:         "E2E project keyword",
				Description:  "test-only project marker",
				Category:     "project",
				MatchType:    "keyword",
				Pattern:      "PROJECT_FALCON",
				Action:       "audit",
				Replacement:  "***",
				Priority:     1,
				Enabled:      true,
				AlertEnabled: true,
			}); err != nil {
				t.Fatalf("seed alert rule: %v", err)
			}
		},
	})

	for i := 0; i < 2; i++ {
		resp, body := env.post("/v1/chat/completions", chatBody("deploy PROJECT_FALCON tonight", 100, false))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	}
	subject := env.mailer.wait(t)
	if !strings.Contains(subject, "E2E project keyword") {
		t.Fatalf("alert subject %q", subject)
	}
	time.Sleep(150 * time.Millisecond)
	if got := env.mailer.count(); got != 1 {
		t.Fatalf("alerts delivered %d, want 1", got)
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	env := newGatewayEnv(t, envSetup{
		upstream: func(w http.ResponseWriter, r *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			close(started)
			select {
			case <-r.Context().Done():
				close(canceled)
			case <-time.After(10 * time.Second):
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		env.http.URL+"/v1/chat/completions", bytes.NewReader(chatBody("stream", 10, true)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(companyTokenHeader, "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not reached")
	}
	cancel()

	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("client cancellation did not cancel the upstream request")
	}

	record := env.waitAudit(t, func(r *store.AuditRecord) bool {
		return r.ClientDisconnected
	})
	if record.StatusCode != http.StatusOK && record.StatusCode != 499 {
		t.Fatalf("client-disconnect status %d", record.StatusCode)
	}
}

func TestUpstreamFailuresReturnBadGatewayAndTimeout(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		env := newGatewayEnv(t, envSetup{
			upstream: func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					return
				}
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
				}
			},
		})
		resp, body := env.post("/v1/chat/completions", chatBody("hello", 10, false))
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		env := newGatewayEnv(t, envSetup{
			providerTimeout: 1,
			upstream: func(_ http.ResponseWriter, r *http.Request, _ []byte) {
				select {
				case <-time.After(10 * time.Second):
				case <-r.Context().Done():
				}
			},
		})
		resp, body := env.post("/v1/chat/completions", chatBody("hello", 10, false))
		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("status %d body %s", resp.StatusCode, body)
		}
	})
}

func TestCrossProtocolRequestIsNeverForwarded(t *testing.T) {
	env := newGatewayEnv(t, envSetup{protocol: "anthropic"})

	resp, body := env.post("/v1/chat/completions", chatBody("hello", 10, false))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"code":"no_route"`) {
		t.Fatalf("body %s", body)
	}
	env.waitNoRequest(t)
}

func TestAdminAPIMountedWithSessionCSRFAndRoles(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})

	admin := newAdminClient(t, env.http.URL)
	resp := admin.login("admin", "bootstrap-password-123")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d", resp.StatusCode)
	}
	meResp, meBody := admin.request(http.MethodGet, "/admin/api/auth/me", nil)
	if meResp.StatusCode != http.StatusOK || !strings.Contains(string(meBody), `"role":"super_admin"`) {
		t.Fatalf("me status %d body %s", meResp.StatusCode, meBody)
	}

	createResp, createBody := admin.request(http.MethodPost, "/admin/api/employees",
		[]byte(`{"employee_no":"E2E-ADMIN","name":"Created by admin"}`))
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create employee status %d body %s", createResp.StatusCode, createBody)
	}

	// The same write without the CSRF header must fail.
	noCSRF := &http.Client{Jar: admin.client.Jar, Timeout: requestTimeout}
	req, err := http.NewRequest(http.MethodPost, env.http.URL+"/admin/api/employees",
		strings.NewReader(`{"employee_no":"E2E-NOCSRF","name":"No CSRF"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	noCSRFResp, err := noCSRF.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	noCSRFResp.Body.Close()
	if noCSRFResp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status %d", noCSRFResp.StatusCode)
	}

	createAdmin(t, context.Background(), env.pg, "auditor-e2e", "auditor-password-123", "auditor")
	auditor := newAdminClient(t, env.http.URL)
	auditorResp := auditor.login("auditor-e2e", "auditor-password-123")
	if auditorResp.StatusCode != http.StatusOK {
		t.Fatalf("auditor login status %d", auditorResp.StatusCode)
	}
	listResp, listBody := auditor.request(http.MethodGet, "/admin/api/employees", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("auditor read status %d body %s", listResp.StatusCode, listBody)
	}
	deniedResp, deniedBody := auditor.request(http.MethodPost, "/admin/api/employees",
		[]byte(`{"employee_no":"E2E-DENIED","name":"Denied"}`))
	if deniedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("auditor write status %d body %s", deniedResp.StatusCode, deniedBody)
	}
}

func TestAdminLoginRejectsBadPassword(t *testing.T) {
	env := newGatewayEnv(t, envSetup{})
	admin := newAdminClient(t, env.http.URL)
	resp := admin.login("admin", "wrong-password")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// newTestCipher builds the same key the environment uses.
func newTestCipher() (*crypto.Cipher, error) {
	return crypto.NewCipher(bytes.Repeat([]byte{0x42}, 32), nil)
}
