package adminapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/company/sluice/internal/alert"
)

type recordingMailer struct {
	cfg     alert.SMTPConfig
	subject string
	body    string
	err     error
}

func (m *recordingMailer) Send(_ context.Context, subject, body string) error {
	m.subject = subject
	m.body = body
	return m.err
}

func TestSettingsRequireSuperAdmin(t *testing.T) {
	env := newAdminEnv(t)
	endpoints := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/api/settings/smtp", ""},
		{http.MethodPut, "/admin/api/settings/smtp", `{}`},
		{http.MethodPost, "/admin/api/settings/smtp/test", `{}`},
		{http.MethodGet, "/admin/api/settings/audit", ""},
		{http.MethodPut, "/admin/api/settings/audit", `{}`},
	}
	for _, role := range []string{"operator", "auditor"} {
		for _, endpoint := range endpoints {
			rr := env.do(role, endpoint.method, endpoint.path, endpoint.body)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s %s %s got %d: %s", role, endpoint.method, endpoint.path, rr.Code, rr.Body.String())
			}
		}
	}
}

func TestSMTPPasswordNeverReturnedAndRetained(t *testing.T) {
	env := newAdminEnv(t)
	first := `{"host":"smtp.example.com","port":587,"username":"u",` +
		`"password":"smtp-secret","from":"gateway@example.com",` +
		`"to":["ops@example.com"],"use_tls":true}`
	rr := env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", first)
	if rr.Code != http.StatusOK {
		t.Fatalf("put got %d: %s", rr.Code, rr.Body.String())
	}
	var dto smtpSettingsDTO
	decodeData(t, rr, &dto)
	if !dto.PasswordSet || dto.Host != "smtp.example.com" || dto.Port != 587 || dto.Username != "u" {
		t.Fatalf("dto %+v", dto)
	}

	rr = env.do("super_admin", http.MethodGet, "/admin/api/settings/smtp", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{"smtp-secret", `"password":`, `"password_enc"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("GET leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"password_set":true`) {
		t.Fatalf("missing password_set: %s", body)
	}

	raw, err := env.store.Settings.Get(context.Background(), "smtp")
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored["password"]; ok {
		t.Fatalf("stored plaintext password: %s", raw)
	}
	var passwordEnc string
	if err := json.Unmarshal(stored["password_enc"], &passwordEnc); err != nil {
		t.Fatalf("password_enc missing: %s", raw)
	}
	sealed, err := base64.StdEncoding.DecodeString(passwordEnc)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := env.cipher.Open(sealed, smtpPasswordAAD)
	if err != nil || string(plain) != "smtp-secret" {
		t.Fatalf("decrypted %q err=%v", plain, err)
	}

	second := `{"host":"smtp.example.com","port":465,"username":"u",` +
		`"password":"","from":"gateway@example.com","to":["ops@example.com"],"use_tls":true}`
	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", second)
	if rr.Code != http.StatusOK {
		t.Fatalf("retain put got %d: %s", rr.Code, rr.Body.String())
	}
	raw, _ = env.store.Settings.Get(context.Background(), "smtp")
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err := json.Unmarshal(stored["password_enc"], &retained); err != nil {
		t.Fatal(err)
	}
	if retained != passwordEnc {
		t.Fatal("empty password did not retain the existing envelope")
	}
	var retainedDTO smtpSettingsDTO
	decodeData(t, rr, &retainedDTO)
	if !retainedDTO.PasswordSet || retainedDTO.Host != "smtp.example.com" || retainedDTO.Port != 465 {
		t.Fatalf("retained dto %+v", retainedDTO)
	}

	for _, body := range []string{
		`{"host":"smtp2.example.com","port":465,"username":"u",` +
			`"password":"","from":"gateway@example.com","to":["ops@example.com"],"use_tls":true}`,
		`{"host":"smtp.example.com","port":465,"username":"other",` +
			`"password":"","from":"gateway@example.com","to":["ops@example.com"],"use_tls":true}`,
	} {
		rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("retained password across identity change got %d: %s", rr.Code, rr.Body.String())
		}
	}

	moved := `{"host":"smtp2.example.com","port":465,"username":"u",` +
		`"password":"new-smtp-secret","from":"gateway@example.com",` +
		`"to":["ops@example.com"],"use_tls":true}`
	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", moved)
	if rr.Code != http.StatusOK {
		t.Fatalf("moved put got %d: %s", rr.Code, rr.Body.String())
	}
	raw, _ = env.store.Settings.Get(context.Background(), "smtp")
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	var movedEnc string
	if err := json.Unmarshal(stored["password_enc"], &movedEnc); err != nil {
		t.Fatal(err)
	}
	if movedEnc == passwordEnc {
		t.Fatal("host change did not replace the sealed credential")
	}
	sealed, err = base64.StdEncoding.DecodeString(movedEnc)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = env.cipher.Open(sealed, smtpPasswordAAD)
	if err != nil || string(plain) != "new-smtp-secret" {
		t.Fatalf("decrypted moved password %q err=%v", plain, err)
	}

	cleared := `{"host":"smtp2.example.com","port":25,"username":"","password":"",` +
		`"from":"gateway@example.com","to":["ops@example.com"],"use_tls":false}`
	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", cleared)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear put got %d: %s", rr.Code, rr.Body.String())
	}
	var clearedDTO smtpSettingsDTO
	decodeData(t, rr, &clearedDTO)
	if clearedDTO.PasswordSet {
		t.Fatalf("password still set after clearing username: %+v", clearedDTO)
	}
	raw, _ = env.store.Settings.Get(context.Background(), "smtp")
	if strings.Contains(string(raw), "password_enc") {
		t.Fatalf("cleared password envelope remains: %s", raw)
	}
}

func TestSMTPValidation(t *testing.T) {
	env := newAdminEnv(t)
	bodies := []string{
		`{"host":"","port":587,"from":"gateway@example.com","to":["ops@example.com"]}`,
		`{"host":"smtp.example.com","port":70000,"from":"gateway@example.com","to":["ops@example.com"]}`,
		`{"host":"smtp.example.com:587","port":587,"from":"gateway@example.com","to":["ops@example.com"]}`,
		`{"host":"smtp.example.com","port":587,"from":"not-an-address","to":["ops@example.com"]}`,
		`{"host":"smtp.example.com","port":587,"from":"gateway@example.com","to":[]}`,
		`{"host":"smtp.example.com","port":587,"from":"gateway@example.com","to":["bad"]}`,
		`{"host":"smtp.example.com","port":587,"username":"u","from":"gateway@example.com","to":["ops@example.com"],"use_tls":true}`,
		`{"host":"smtp.example.com","port":587,"password":"secret","from":"gateway@example.com","to":["ops@example.com"],"use_tls":true}`,
		`{"host":"smtp.example.com","port":587,"username":"u","password":"secret","from":"gateway@example.com","to":["ops@example.com"],"use_tls":false}`,
	}
	for _, body := range bodies {
		rr := env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
	rr := env.do("super_admin", http.MethodGet, "/admin/api/settings/smtp", "")
	var dto smtpSettingsDTO
	decodeData(t, rr, &dto)
	if dto.PasswordSet || dto.Host != "" || dto.Port != 587 {
		t.Fatalf("invalid saves changed settings: %+v", dto)
	}
}

func TestSMTPTestEmail(t *testing.T) {
	env := newAdminEnv(t)
	config := `{"host":"smtp.example.com","port":587,"username":"u",` +
		`"password":"smtp-secret","from":"gateway@example.com",` +
		`"to":["ops@example.com"],"use_tls":true}`
	if rr := env.do("super_admin", http.MethodPut, "/admin/api/settings/smtp", config); rr.Code != http.StatusOK {
		t.Fatalf("put got %d: %s", rr.Code, rr.Body.String())
	}

	var sent *recordingMailer
	env.settings.mailerFactory = func(cfg alert.SMTPConfig) alert.Mailer {
		sent = &recordingMailer{cfg: cfg}
		return sent
	}
	rr := env.do("super_admin", http.MethodPost, "/admin/api/settings/smtp/test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("test got %d: %s", rr.Code, rr.Body.String())
	}
	if sent == nil {
		t.Fatal("mailer was not called")
	}
	if sent.cfg.Password != "smtp-secret" || sent.cfg.Host != "smtp.example.com" ||
		len(sent.cfg.To) != 1 || sent.cfg.To[0] != "ops@example.com" {
		t.Fatalf("cfg %+v", sent.cfg)
	}
	if !strings.Contains(sent.subject, "SMTP") || !strings.Contains(sent.body, "测试") {
		t.Fatalf("subject=%q body=%q", sent.subject, sent.body)
	}
	if strings.Contains(sent.body, "smtp-secret") {
		t.Fatalf("test email body leaked the password: %s", sent.body)
	}

	env.settings.mailerFactory = func(cfg alert.SMTPConfig) alert.Mailer {
		return &recordingMailer{cfg: cfg, err: errors.New("connection refused")}
	}
	rr = env.do("super_admin", http.MethodPost, "/admin/api/settings/smtp/test", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("failed test got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "smtp-secret") ||
		strings.Contains(rr.Body.String(), "connection refused") {
		t.Fatalf("failure leaked details: %s", rr.Body.String())
	}
	if count := env.adminAuditCount("test_smtp"); count != 2 {
		t.Fatalf("test_smtp audit rows %d", count)
	}
}

func TestSMTPTestRequiresConfiguration(t *testing.T) {
	env := newAdminEnv(t)
	rr := env.do("super_admin", http.MethodPost, "/admin/api/settings/smtp/test", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	if count := env.adminAuditCount("test_smtp"); count != 0 {
		t.Fatalf("test_smtp audit rows %d", count)
	}
}

func TestAuditSettings(t *testing.T) {
	env := newAdminEnv(t)
	rr := env.do("super_admin", http.MethodGet, "/admin/api/settings/audit", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get got %d: %s", rr.Code, rr.Body.String())
	}
	var defaults auditSettingsDTO
	decodeData(t, rr, &defaults)
	if defaults.RetentionDays != 180 || !defaults.AlertEnabled {
		t.Fatalf("defaults %+v", defaults)
	}

	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/audit",
		`{"retention_days":90,"alert_enabled":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("put got %d: %s", rr.Code, rr.Body.String())
	}
	var updated auditSettingsDTO
	decodeData(t, rr, &updated)
	if updated.RetentionDays != 90 || updated.AlertEnabled {
		t.Fatalf("updated %+v", updated)
	}
	rr = env.do("super_admin", http.MethodGet, "/admin/api/settings/audit", "")
	var persisted auditSettingsDTO
	decodeData(t, rr, &persisted)
	if persisted != updated {
		t.Fatalf("persisted %+v", persisted)
	}

	rr = env.do("super_admin", http.MethodPut, "/admin/api/settings/audit", `{"retention_days":30}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("partial put got %d: %s", rr.Code, rr.Body.String())
	}
	var partial auditSettingsDTO
	decodeData(t, rr, &partial)
	if partial.RetentionDays != 30 || partial.AlertEnabled {
		t.Fatalf("partial %+v", partial)
	}

	for _, body := range []string{
		`{"retention_days":0}`,
		`{"retention_days":-1}`,
		`{"retention_days":3651}`,
	} {
		rr := env.do("super_admin", http.MethodPut, "/admin/api/settings/audit", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
	if count := env.adminAuditCount("update_audit_settings"); count != 2 {
		t.Fatalf("audit setting writes %d", count)
	}

	raw, err := env.store.Settings.Get(context.Background(), "audit")
	if err != nil {
		t.Fatal(err)
	}
	var stored auditSettingsDTO
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.RetentionDays != 30 || stored.AlertEnabled {
		t.Fatalf("stored %+v", stored)
	}
}
