package adminapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/adminauth"
	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

func seedAuditRecord(t *testing.T, env *adminEnv, at time.Time, mutate func(*store.AuditRecord)) *store.AuditRecord {
	t.Helper()
	employeeID, err := uuid.Parse(env.employeeID)
	if err != nil {
		t.Fatal(err)
	}
	record := &store.AuditRecord{
		EventTime:        at.UTC(),
		RequestID:        uuid.NewString(),
		EmployeeID:       &employeeID,
		ClientIP:         "203.0.113.9",
		Method:           http.MethodPost,
		Path:             "/v1/chat/completions",
		Model:            "gpt-4o",
		StatusCode:       http.StatusOK,
		LatencyMS:        120,
		PromptTokens:     60,
		CompletionTokens: 40,
		TotalTokens:      100,
		DLPAction:        "none",
		DLPHits:          []byte("[]"),
	}
	if mutate != nil {
		mutate(record)
	}
	if err := env.store.Audit.InsertBatch(context.Background(), []*store.AuditRecord{record}); err != nil {
		t.Fatalf("seed audit record: %v", err)
	}
	if err := env.pool.QueryRow(context.Background(),
		`SELECT id FROM audit_logs WHERE request_id=$1`, record.RequestID).Scan(&record.ID); err != nil {
		t.Fatalf("load audit id: %v", err)
	}
	return record
}

func sealAuditField(t *testing.T, env *adminEnv, aad, value string) []byte {
	t.Helper()
	sealed, err := env.cipher.Seal([]byte(value), aad)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func (e *adminEnv) adminAuditCount(action string) int {
	e.t.Helper()
	var count int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM admin_audit_logs WHERE action=$1`, action).Scan(&count); err != nil {
		e.t.Fatalf("count admin audit: %v", err)
	}
	return count
}

func (e *adminEnv) adminAuditDetail(action string) (map[string]any, string) {
	e.t.Helper()
	var detail []byte
	var clientIP string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT detail, coalesce(host(client_ip),'') FROM admin_audit_logs
		 WHERE action=$1 ORDER BY id DESC LIMIT 1`, action).Scan(&detail, &clientIP); err != nil {
		e.t.Fatalf("load admin audit: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(detail, &decoded); err != nil {
		e.t.Fatalf("decode admin audit detail: %v", err)
	}
	return decoded, clientIP
}

func TestRevealRequiresSuperAdminAndReason(t *testing.T) {
	env := newAdminEnv(t)
	record := seedAuditRecord(t, env, time.Now().Add(-time.Hour), func(r *store.AuditRecord) {
		r.PromptEnc = sealAuditField(t, env, r.RequestID+":prompt", "secret prompt")
		r.ResponseEnc = sealAuditField(t, env, r.RequestID+":response", "secret response")
	})
	path := fmt.Sprintf("/admin/api/audit/%d/reveal", record.ID)

	for _, role := range []string{"auditor", "operator"} {
		rr := env.do(role, http.MethodPost, path, `{"reason":"investigation"}`)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s got %d: %s", role, rr.Code, rr.Body.String())
		}
	}
	for _, body := range []string{
		`{"reason":""}`,
		`{"reason":"abcd"}`,
		`{"reason":"原因"}`,
		`{"reason":"   "}`,
	} {
		rr := env.do("super_admin", http.MethodPost, path, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("reason %s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
	if count := env.adminAuditCount("reveal_audit"); count != 0 {
		t.Fatalf("rejected reveals wrote %d admin audit rows", count)
	}
}

func TestRevealWritesAdminAudit(t *testing.T) {
	env := newAdminEnv(t)
	record := seedAuditRecord(t, env, time.Now().Add(-time.Hour), func(r *store.AuditRecord) {
		r.PromptEnc = sealAuditField(t, env, r.RequestID+":prompt", "secret prompt")
		r.ResponseEnc = sealAuditField(t, env, r.RequestID+":response", "secret response")
	})

	rr := env.do("super_admin", http.MethodPost,
		fmt.Sprintf("/admin/api/audit/%d/reveal", record.ID), `{"reason":"customer complaint"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("reveal got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("Cache-Control=%q", rr.Header().Get("Cache-Control"))
	}
	var revealed struct {
		Prompt   string `json:"prompt"`
		Response string `json:"response"`
	}
	decodeData(t, rr, &revealed)
	if revealed.Prompt != "secret prompt" || revealed.Response != "secret response" {
		t.Fatalf("revealed %+v", revealed)
	}
	if count := env.adminAuditCount("reveal_audit"); count != 1 {
		t.Fatalf("admin audit rows %d", count)
	}
	detail, _ := env.adminAuditDetail("reveal_audit")
	if detail["reason"] != "customer complaint" {
		t.Fatalf("reason %v", detail["reason"])
	}
	fields, _ := detail["fields"].([]any)
	if len(fields) != 2 || fields[0] != "prompt" || fields[1] != "response" {
		t.Fatalf("fields %v", detail["fields"])
	}
	encoded, _ := json.Marshal(detail)
	if strings.Contains(string(encoded), "secret prompt") || strings.Contains(string(encoded), "secret response") {
		t.Fatalf("admin audit detail leaked plaintext: %s", encoded)
	}
}

func TestAuditListAndDetailNeverReturnPlaintext(t *testing.T) {
	env := newAdminEnv(t)
	record := seedAuditRecord(t, env, time.Now().Add(-time.Hour), func(r *store.AuditRecord) {
		r.PromptEnc = sealAuditField(t, env, r.RequestID+":prompt", "secret prompt")
		r.ResponseEnc = sealAuditField(t, env, r.RequestID+":response", "secret response")
		r.DLPAction = "block"
		r.DLPHits = []byte(`[{"rule_id":"r1","rule_name":"aws_access_key","action":"block","alert_enabled":true,"start":10,"end":30}]`)
	})
	promptCiphertext := base64.StdEncoding.EncodeToString(record.PromptEnc)

	rr := env.do("auditor", http.MethodGet, "/admin/api/audit", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{
		"secret prompt", "secret response", promptCiphertext, `"prompt_enc"`, `"response_enc"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, body)
		}
	}
	var list struct {
		Items      []auditLogDTO `json:"items"`
		NextCursor string        `json:"next_cursor"`
	}
	decodeData(t, rr, &list)
	if len(list.Items) != 1 {
		t.Fatalf("items %d", len(list.Items))
	}
	item := list.Items[0]
	if !item.HasPrompt || !item.HasResponse {
		t.Fatalf("content flags %+v", item)
	}
	if len(item.DLPHits) != 1 || item.DLPHits[0].RuleName != "aws_access_key" ||
		item.DLPHits[0].Action != "block" {
		t.Fatalf("dlp hits %+v", item.DLPHits)
	}

	rr = env.do("auditor", http.MethodGet, fmt.Sprintf("/admin/api/audit/%d", record.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("detail got %d: %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	for _, forbidden := range []string{
		"secret prompt", "secret response", promptCiphertext, `"prompt_enc"`, `"response_enc"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("detail leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"has_prompt":true`) {
		t.Fatalf("detail missing has_prompt: %s", body)
	}
}

func TestAuditListFiltersAndPagination(t *testing.T) {
	env := newAdminEnv(t)
	now := time.Now()
	old := seedAuditRecord(t, env, now.Add(-30*time.Hour), nil)
	first := seedAuditRecord(t, env, now.Add(-3*time.Hour), func(r *store.AuditRecord) {
		r.StatusCode = 200
	})
	second := seedAuditRecord(t, env, now.Add(-2*time.Hour), func(r *store.AuditRecord) {
		r.Model = "claude-sonnet"
		r.StatusCode = 500
		r.DLPAction = "block"
	})
	third := seedAuditRecord(t, env, now.Add(-time.Hour), func(r *store.AuditRecord) {
		r.Model = "claude-sonnet"
		r.DLPAction = "mask"
	})

	rr := env.do("auditor", http.MethodGet, "/admin/api/audit?limit=2", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("page 1 got %d: %s", rr.Code, rr.Body.String())
	}
	var page1 struct {
		Items      []auditLogDTO `json:"items"`
		NextCursor string        `json:"next_cursor"`
	}
	decodeData(t, rr, &page1)
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1 %+v", page1)
	}
	if page1.Items[0].ID != third.ID || page1.Items[1].ID != second.ID {
		t.Fatalf("page 1 ids %d %d", page1.Items[0].ID, page1.Items[1].ID)
	}
	if page1.Items[0].ID == old.ID {
		t.Fatal("default 24h range included a 30h-old record")
	}

	rr = env.do("auditor", http.MethodGet,
		"/admin/api/audit?limit=2&cursor="+url.QueryEscape(page1.NextCursor), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("page 2 got %d: %s", rr.Code, rr.Body.String())
	}
	var page2 struct {
		Items      []auditLogDTO `json:"items"`
		NextCursor string        `json:"next_cursor"`
	}
	decodeData(t, rr, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID != first.ID || page2.NextCursor != "" {
		t.Fatalf("page 2 %+v", page2)
	}

	cases := []struct {
		query string
		want  []int64
	}{
		{"status_code=500", []int64{second.ID}},
		{"dlp_action=block", []int64{second.ID}},
		{"model=claude-sonnet", []int64{third.ID, second.ID}},
		{"employee_id=" + env.employeeID, []int64{third.ID, second.ID, first.ID}},
	}
	for _, tc := range cases {
		rr := env.do("auditor", http.MethodGet, "/admin/api/audit?"+tc.query, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s got %d: %s", tc.query, rr.Code, rr.Body.String())
		}
		var page struct {
			Items []auditLogDTO `json:"items"`
		}
		decodeData(t, rr, &page)
		if len(page.Items) != len(tc.want) {
			t.Fatalf("%s got %d items", tc.query, len(page.Items))
		}
		for i, id := range tc.want {
			if page.Items[i].ID != id {
				t.Fatalf("%s item %d = %d, want %d", tc.query, i, page.Items[i].ID, id)
			}
		}
	}

	from := first.EventTime.Add(time.Minute).Format(time.RFC3339)
	to := third.EventTime.Add(time.Minute).Format(time.RFC3339)
	rr = env.do("auditor", http.MethodGet,
		"/admin/api/audit?from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("range got %d: %s", rr.Code, rr.Body.String())
	}
	var ranged struct {
		Items []auditLogDTO `json:"items"`
	}
	decodeData(t, rr, &ranged)
	if len(ranged.Items) != 2 || ranged.Items[0].ID != third.ID || ranged.Items[1].ID != second.ID {
		t.Fatalf("ranged %+v", ranged.Items)
	}

	for _, bad := range []string{
		"cursor=not-a-cursor",
		"status_code=99",
		"status_code=600",
		"status_code=abc",
		"dlp_action=deny",
		"limit=0",
		"limit=-1",
		"limit=abc",
		"from=garbage",
		"from=2026-11-02T10:00:00Z&to=2026-11-02T10:00:00Z",
	} {
		rr := env.do("auditor", http.MethodGet, "/admin/api/audit?"+bad, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", bad, rr.Code, rr.Body.String())
		}
	}

	rr = env.do("auditor", http.MethodGet, "/admin/api/audit/999999", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing detail got %d: %s", rr.Code, rr.Body.String())
	}
}

type revealAuditStub struct {
	store.AuditStore
	record *store.AuditRecord
	err    error
}

func (s *revealAuditStub) Get(context.Context, int64) (*store.AuditRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.record, nil
}

type revealAdminStub struct {
	store.AdminStore
	err   error
	calls int
}

func (s *revealAdminStub) InsertAdminAudit(context.Context, *store.AdminAuditLog) error {
	s.calls++
	return s.err
}

func newRevealTestEngine(handler *AuditHandler, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(adminauth.ContextUser, &store.AdminUser{ID: uuid.New(), Role: role})
		c.Next()
	})
	r.POST("/audit/:id/reveal", handler.Reveal)
	return r
}

func TestRevealFailsClosedWhenAdminAuditWriteFails(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 32)
	cipher, err := crypto.NewCipher(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	prompt, err := cipher.Seal([]byte("secret prompt"), requestID+":prompt")
	if err != nil {
		t.Fatal(err)
	}
	response, err := cipher.Seal([]byte("secret response"), requestID+":response")
	if err != nil {
		t.Fatal(err)
	}
	admins := &revealAdminStub{err: errors.New("database down")}
	handler := NewAuditHandler(&revealAuditStub{record: &store.AuditRecord{
		ID: 1, RequestID: requestID, PromptEnc: prompt, ResponseEnc: response,
	}}, admins, cipher, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/audit/1/reveal",
		strings.NewReader(`{"reason":"customer complaint"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	newRevealTestEngine(handler, "super_admin").ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	if admins.calls != 1 {
		t.Fatalf("admin audit attempts %d", admins.calls)
	}
	if strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("failed reveal leaked plaintext: %s", rr.Body.String())
	}
}

func TestRevealRejectsOperatorAtHandler(t *testing.T) {
	admins := &revealAdminStub{}
	handler := NewAuditHandler(&revealAuditStub{record: &store.AuditRecord{ID: 1}},
		admins, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/audit/1/reveal",
		strings.NewReader(`{"reason":"customer complaint"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	newRevealTestEngine(handler, "operator").ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	if admins.calls != 0 {
		t.Fatalf("operator reveal attempted admin audit %d times", admins.calls)
	}
}

func TestRevealDecryptionFailureDoesNotReturnPlaintext(t *testing.T) {
	goodCipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x44}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	otherCipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x55}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	prompt, err := otherCipher.Seal([]byte("secret prompt"), requestID+":prompt")
	if err != nil {
		t.Fatal(err)
	}
	admins := &revealAdminStub{}
	handler := NewAuditHandler(&revealAuditStub{record: &store.AuditRecord{
		ID: 1, RequestID: requestID, PromptEnc: prompt,
	}}, admins, goodCipher, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/audit/1/reveal",
		strings.NewReader(`{"reason":"customer complaint"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	newRevealTestEngine(handler, "super_admin").ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	if admins.calls != 0 {
		t.Fatalf("admin audit written for failed decryption")
	}
	if strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("decryption failure leaked plaintext: %s", rr.Body.String())
	}
}
