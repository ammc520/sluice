package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/company/sluice/internal/app"
	"github.com/company/sluice/internal/config"
	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
	"github.com/company/sluice/internal/store/postgres"
	"github.com/company/sluice/internal/testutil"
)

const (
	companyTokenHeader   = "Authorization"
	requestTimeout       = 10 * time.Second
	streamRequestTimeout = 30 * time.Second
)

type capturedRequest struct {
	Path   string
	Header http.Header
	Body   []byte
}

type upstreamHandler func(http.ResponseWriter, *http.Request, []byte)

type envSetup struct {
	protocol        string
	providerTimeout int
	bodyLimit       int64
	upstream        upstreamHandler
	// seed runs after the base employee/token/provider rows are inserted and
	// before the application loads its runtime snapshots.
	seed func(context.Context, *postgres.Store, *gatewayEnv)
}

type gatewayEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	pg       *postgres.Store
	app      *app.App
	http     *httptest.Server
	upstream *httptest.Server
	requests chan capturedRequest
	mailer   *captureMailer

	token      string
	tokenID    uuid.UUID
	employeeID uuid.UUID
	providerID uuid.UUID
	cfg        *config.Config

	closeOnce sync.Once
}

type captureMailer struct {
	mu       sync.Mutex
	subjects []string
	ch       chan string
}

func (m *captureMailer) Send(_ context.Context, subject, _ string) error {
	m.mu.Lock()
	m.subjects = append(m.subjects, subject)
	m.mu.Unlock()
	select {
	case m.ch <- subject:
	default:
	}
	return nil
}

func (m *captureMailer) wait(t *testing.T) string {
	t.Helper()
	select {
	case subject := <-m.ch:
		return subject
	case <-time.After(5 * time.Second):
		t.Fatal("no DLP alert email was delivered")
		return ""
	}
}

func (m *captureMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subjects)
}

func newGatewayEnv(t *testing.T, setup envSetup) *gatewayEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	if setup.protocol == "" {
		setup.protocol = "openai_compatible"
	}
	if setup.providerTimeout <= 0 {
		setup.providerTimeout = 5
	}
	if setup.bodyLimit <= 0 {
		setup.bodyLimit = 2 * 1024 * 1024
	}
	if setup.upstream == nil {
		setup.upstream = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-ok","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":7,"total_tokens":11}}`)
		}
	}

	pool := testutil.OpenTestDB(t)
	pg := postgres.New(pool)
	ctx := context.Background()

	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x42}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}

	employee := &store.Employee{
		EmployeeNo: "E2E-001",
		Name:       "E2E User",
		Email:      "e2e@example.com",
		Status:     1,
	}
	if err := pg.Employees.Create(ctx, employee); err != nil {
		t.Fatalf("seed employee: %v", err)
	}

	plain, hash, prefix, last4, err := crypto.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	quotaTotal := int64(1_000_000)
	tokenID := uuid.New()
	if err := pg.Tokens.Create(ctx, &store.Token{
		ID:           tokenID,
		EmployeeID:   employee.ID,
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TokenLast4:   last4,
		QuotaType:    "tokens",
		QuotaPeriod:  "monthly",
		QuotaTotal:   &quotaTotal,
		RateLimitRPM: 10_000,
		IPAllowlist:  []string{},
		Status:       1,
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	requests := make(chan capturedRequest, 512)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case requests <- capturedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}:
		default:
		}
		setup.upstream(w, r, body)
	}))
	t.Cleanup(upstream.Close)

	providerID := uuid.New()
	sealed, err := cipher.Seal([]byte("upstream-secret-key"), "provider:"+providerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Providers.Create(ctx, &store.Provider{
		ID:             providerID,
		Name:           "e2e-" + providerID.String(),
		Protocol:       setup.protocol,
		BaseURL:        upstream.URL,
		APIKeyEnc:      sealed,
		IsDefault:      true,
		Enabled:        true,
		TimeoutSeconds: setup.providerTimeout,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	cfg := &config.Config{
		ListenAddr:             ":0",
		DatabaseURL:            os.Getenv("TEST_DATABASE_URL"),
		MasterKey:              bytes.Repeat([]byte{0x42}, 32),
		CookieSecure:           false,
		AllowPrivateUpstreams:  true,
		RequestBodyLimit:       setup.bodyLimit,
		CaptureLimit:           256 * 1024,
		AuditSpoolDir:          t.TempDir(),
		BootstrapAdminUsername: "admin",
		BootstrapAdminPassword: "bootstrap-password-123",
		DefaultReserveTokens:   4096,
		AdminSessionTTLHours:   1,
	}
	env := &gatewayEnv{
		t:          t,
		pool:       pool,
		pg:         pg,
		upstream:   upstream,
		requests:   requests,
		mailer:     &captureMailer{ch: make(chan string, 8)},
		token:      plain,
		tokenID:    tokenID,
		employeeID: employee.ID,
		providerID: providerID,
		cfg:        cfg,
	}
	if setup.seed != nil {
		setup.seed(ctx, pg, env)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	application, err := app.New(ctx, cfg, logger, app.Options{
		DisableRedis:       true,
		AlertMailer:        env.mailer,
		AuditQueueSize:     1024,
		AuditFlushInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	env.app = application
	env.http = httptest.NewServer(application.Server.Handler())
	t.Cleanup(env.shutdown)
	return env
}

func (e *gatewayEnv) shutdown() {
	e.closeOnce.Do(func() {
		if e.http != nil {
			e.http.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := e.app.Close(ctx); err != nil {
			e.t.Errorf("close app: %v", err)
		}
	})
}

func (e *gatewayEnv) do(ctx context.Context, method, path string, body []byte, token string) (*http.Response, []byte) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.http.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(companyTokenHeader, "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: streamRequestTimeout}).Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read response: %v", err)
	}
	return resp, out
}

func (e *gatewayEnv) post(path string, body []byte) (*http.Response, []byte) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	return e.do(ctx, http.MethodPost, path, body, e.token)
}

func (e *gatewayEnv) auditRecords() []*store.AuditRecord {
	e.t.Helper()
	records, err := e.pg.Audit.List(context.Background(), store.AuditFilter{Limit: 100})
	if err != nil {
		e.t.Fatalf("list audit records: %v", err)
	}
	return records
}

func (e *gatewayEnv) waitAudit(t *testing.T, match func(*store.AuditRecord) bool) *store.AuditRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records := e.auditRecords()
		for _, record := range records {
			if match(record) {
				return record
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("audit record not persisted; records: %+v", e.auditRecords())
	return nil
}

func (e *gatewayEnv) waitNoRequest(t *testing.T) {
	t.Helper()
	select {
	case req := <-e.requests:
		t.Fatalf("upstream received an unexpected request: %s %s", req.Path, req.Body)
	case <-time.After(300 * time.Millisecond):
	}
}

func (e *gatewayEnv) nextRequest(t *testing.T) capturedRequest {
	t.Helper()
	select {
	case req := <-e.requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not receive a request")
		return capturedRequest{}
	}
}

func chatBody(content string, maxTokens int64, stream bool) []byte {
	payload := map[string]any{
		"model": "test-model",
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
	}
	if maxTokens > 0 {
		payload["max_tokens"] = maxTokens
	}
	if stream {
		payload["stream"] = true
	}
	body, _ := json.Marshal(payload)
	return body
}

func openAIStream(chunks int) upstreamHandler {
	return func(w http.ResponseWriter, r *http.Request, _ []byte) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < chunks; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chunk-%d\"}}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":7,\"total_tokens\":11}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func createAdmin(t *testing.T, ctx context.Context, pg *postgres.Store, username, password, role string) {
	t.Helper()
	hash, err := crypto.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Admins.Create(ctx, &store.AdminUser{
		Username: username, PasswordHash: hash, Role: role, Status: 1,
	}); err != nil {
		t.Fatalf("create admin %s: %v", username, err)
	}
}

type adminClient struct {
	t      *testing.T
	base   string
	client *http.Client
	csrf   string
}

func newAdminClient(t *testing.T, base string) *adminClient {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &adminClient{
		t:      t,
		base:   base,
		client: &http.Client{Jar: jar, Timeout: requestTimeout},
	}
}

func (a *adminClient) login(username, password string) *http.Response {
	a.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := a.client.Post(a.base+"/admin/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	base, err := url.Parse(a.base)
	if err != nil {
		a.t.Fatal(err)
	}
	// The CSRF cookie is scoped to /admin, so look it up as the SPA would
	// rather than from the bare origin.
	adminURL := base.ResolveReference(&url.URL{Path: "/admin/"})
	for _, cookie := range a.client.Jar.Cookies(adminURL) {
		if cookie.Name == "csrf_token" {
			a.csrf = cookie.Value
		}
	}
	return resp
}

func (a *adminClient) request(method, path string, body []byte) (*http.Response, []byte) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.base+path, bytes.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}
