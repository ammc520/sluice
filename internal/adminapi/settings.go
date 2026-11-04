package adminapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/adminauth"
	"github.com/company/sluice/internal/alert"
	"github.com/company/sluice/internal/auth"
	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/portalcfg"
	"github.com/company/sluice/internal/store"
)

const (
	smtpSettingsKey  = "smtp"
	smtpPasswordAAD  = "setting:smtp.password"
	auditSettingsKey = "audit"

	defaultSMTPPort          = 587
	defaultAuditRetention    = 180
	defaultAuditAlertEnabled = true
	maxAuditRetentionDays    = 3650
	maxSMTPPasswordLength    = 1024
	maxSMTPRecipients        = 100
)

// SettingsHandler serves SMTP and audit settings endpoints. Only super
// administrators may use these routes.
type SettingsHandler struct {
	settings         store.SettingsStore
	admins           store.AdminStore
	cipher           *crypto.Cipher
	mailerFactory    func(alert.SMTPConfig) alert.Mailer
	now              func() time.Time
	trustedProxies   []string
	portalInvalidate func()
}

// NewSettingsHandler creates the settings handler.
func NewSettingsHandler(settings store.SettingsStore, admins store.AdminStore, cipher *crypto.Cipher, mailerFactory func(alert.SMTPConfig) alert.Mailer, clock func() time.Time, trustedProxies []string) *SettingsHandler {
	if clock == nil {
		clock = time.Now
	}
	if mailerFactory == nil {
		mailerFactory = alert.NewMailer
	}
	return &SettingsHandler{
		settings:       settings,
		admins:         admins,
		cipher:         cipher,
		mailerFactory:  mailerFactory,
		now:            clock,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

// SetPortalInvalidate lets the application drop the cached portal policy as
// soon as an administrator saves it.
func (h *SettingsHandler) SetPortalInvalidate(fn func()) {
	h.portalInvalidate = fn
}

// requireSuperAdmin is a defense-in-depth check. Routes also use
// adminauth.RequireRole("super_admin").
func requireSuperAdmin(c *gin.Context) bool {
	user := adminauth.CurrentUser(c)
	if user == nil {
		Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		return false
	}
	if user.Role != "super_admin" {
		Fail(c, http.StatusForbidden, "forbidden", "insufficient role")
		return false
	}
	return true
}

// recordAdminAudit writes an administrator action. Detail must never contain
// plaintext credentials or sensitive prompt/response content.
func recordAdminAudit(c *gin.Context, admins store.AdminStore, trustedProxies []string, action, targetType, targetID string, detail map[string]any) error {
	user := adminauth.CurrentUser(c)
	if user == nil {
		return errors.New("adminapi: missing authenticated administrator")
	}
	payload := []byte("{}")
	if detail != nil {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		payload = encoded
	}
	return admins.InsertAdminAudit(c.Request.Context(), &store.AdminAuditLog{
		AdminID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     payload,
		ClientIP:   auth.ClientIP(c.Request, trustedProxies),
	})
}

type smtpSettingsStored struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username,omitempty"`
	PasswordEnc string   `json:"password_enc,omitempty"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	UseTLS      bool     `json:"use_tls"`
}

type smtpSettingsDTO struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	PasswordSet bool     `json:"password_set"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	UseTLS      bool     `json:"use_tls"`
}

func (s smtpSettingsStored) dto() smtpSettingsDTO {
	port := s.Port
	if port == 0 {
		port = defaultSMTPPort
	}
	to := s.To
	if to == nil {
		to = []string{}
	}
	return smtpSettingsDTO{
		Host:        s.Host,
		Port:        port,
		Username:    s.Username,
		PasswordSet: s.PasswordEnc != "",
		From:        s.From,
		To:          to,
		UseTLS:      s.UseTLS,
	}
}

type smtpSettingsPayload struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	UseTLS   bool     `json:"use_tls"`
}

func (h *SettingsHandler) loadSMTPSettings(ctx context.Context) (smtpSettingsStored, bool, error) {
	raw, err := h.settings.Get(ctx, smtpSettingsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return smtpSettingsStored{}, false, nil
		}
		return smtpSettingsStored{}, false, err
	}
	var stored smtpSettingsStored
	if err := json.Unmarshal(raw, &stored); err != nil {
		return smtpSettingsStored{}, true, errors.New("adminapi: invalid SMTP settings")
	}
	return stored, true, nil
}

// GetSMTP handles GET /admin/api/settings/smtp.
func (h *SettingsHandler) GetSMTP(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	stored, _, err := h.loadSMTPSettings(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, stored.dto())
}

// PutSMTP handles PUT /admin/api/settings/smtp. An empty password keeps the
// existing encrypted password when a username is present.
func (h *SettingsHandler) PutSMTP(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	var payload smtpSettingsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Host = strings.TrimSpace(payload.Host)
	payload.Username = strings.TrimSpace(payload.Username)
	payload.From = strings.TrimSpace(payload.From)
	if payload.Port == 0 {
		payload.Port = defaultSMTPPort
	}
	if len(payload.Password) > maxSMTPPasswordLength {
		BadRequest(c, "password is too long")
		return
	}
	if len(payload.To) > maxSMTPRecipients {
		BadRequest(c, "too many recipients")
		return
	}
	recipients := make([]string, 0, len(payload.To))
	for _, raw := range payload.To {
		recipients = append(recipients, strings.TrimSpace(raw))
	}
	payload.To = recipients

	existing, _, err := h.loadSMTPSettings(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	cfg := alert.SMTPConfig{
		Host:     payload.Host,
		Port:     payload.Port,
		Username: payload.Username,
		Password: payload.Password,
		From:     payload.From,
		To:       append([]string(nil), recipients...),
		UseTLS:   payload.UseTLS,
	}
	passwordChanged := payload.Password != ""
	// A retained credential is only valid for the SMTP endpoint and identity
	// it was entered for. Requiring re-entry on either change prevents a
	// settings edit from redirecting the stored secret to another server.
	if !passwordChanged && existing.PasswordEnc != "" && payload.Username != "" &&
		(payload.Host != existing.Host || payload.Username != existing.Username) {
		BadRequest(c, "password must be re-entered when SMTP host or username changes")
		return
	}
	if cfg.Password == "" && cfg.Username != "" && existing.PasswordEnc != "" {
		// Validation only needs a non-empty placeholder to confirm that the
		// retained password is paired with a username and TLS.
		cfg.Password = "retained"
	}
	if err := alert.ValidateSMTPConfig(cfg); err != nil {
		BadRequest(c, strings.TrimPrefix(err.Error(), "alert: "))
		return
	}

	passwordEnc := ""
	switch {
	case payload.Username == "":
		passwordEnc = ""
	case passwordChanged:
		if h.cipher == nil {
			InternalError(c)
			return
		}
		sealed, err := h.cipher.Seal([]byte(payload.Password), smtpPasswordAAD)
		if err != nil {
			InternalError(c)
			return
		}
		passwordEnc = base64.StdEncoding.EncodeToString(sealed)
	default:
		passwordEnc = existing.PasswordEnc
	}

	stored := smtpSettingsStored{
		Host:        payload.Host,
		Port:        payload.Port,
		Username:    payload.Username,
		PasswordEnc: passwordEnc,
		From:        payload.From,
		To:          append([]string(nil), recipients...),
		UseTLS:      payload.UseTLS,
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		InternalError(c)
		return
	}
	if err := h.settings.Set(c.Request.Context(), smtpSettingsKey, raw); err != nil {
		InternalError(c)
		return
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "update_smtp_settings", "settings", smtpSettingsKey, map[string]any{
		"host":             stored.Host,
		"port":             stored.Port,
		"from":             stored.From,
		"use_tls":          stored.UseTLS,
		"password_changed": passwordChanged,
	}); err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, stored.dto())
}

// TestSMTP handles POST /admin/api/settings/smtp/test.
func (h *SettingsHandler) TestSMTP(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	cfg, err := alert.LoadSMTP(c.Request.Context(), h.settings, h.cipher)
	if err != nil {
		if errors.Is(err, alert.ErrSMTPNotConfigured) {
			BadRequest(c, "SMTP is not configured")
			return
		}
		InternalError(c)
		return
	}
	mailer := h.mailerFactory(cfg)
	if mailer == nil {
		InternalError(c)
		return
	}
	body := fmt.Sprintf("这是一封来自 API 安全网关的 SMTP 测试邮件。\n时间: %s\n",
		h.now().In(shanghaiLocation()).Format("2006-01-02 15:04:05 -0700"))
	sendErr := mailer.Send(c.Request.Context(), "[API Gateway] SMTP 测试", body)
	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "test_smtp", "settings", smtpSettingsKey, map[string]any{
		"success": sendErr == nil,
	})
	if sendErr != nil {
		Fail(c, http.StatusBadGateway, "smtp_failed", "SMTP test delivery failed")
		return
	}
	OK(c, http.StatusOK, gin.H{"sent": true})
}

type auditSettingsDTO struct {
	RetentionDays int  `json:"retention_days"`
	AlertEnabled  bool `json:"alert_enabled"`
}

type auditSettingsPayload struct {
	RetentionDays *int  `json:"retention_days"`
	AlertEnabled  *bool `json:"alert_enabled"`
}

func (h *SettingsHandler) loadAuditSettings(ctx context.Context) (auditSettingsDTO, error) {
	out := auditSettingsDTO{
		RetentionDays: defaultAuditRetention,
		AlertEnabled:  defaultAuditAlertEnabled,
	}
	raw, err := h.settings.Get(ctx, auditSettingsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return out, nil
		}
		return auditSettingsDTO{}, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return auditSettingsDTO{}, errors.New("adminapi: invalid audit settings")
	}
	if out.RetentionDays <= 0 {
		out.RetentionDays = defaultAuditRetention
	}
	return out, nil
}

// GetAuditSettings handles GET /admin/api/settings/audit.
func (h *SettingsHandler) GetAuditSettings(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	settings, err := h.loadAuditSettings(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, settings)
}

// PutAuditSettings handles PUT /admin/api/settings/audit. Omitted fields keep
// their current values.
func (h *SettingsHandler) PutAuditSettings(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	var payload auditSettingsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	settings, err := h.loadAuditSettings(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	if payload.RetentionDays != nil {
		settings.RetentionDays = *payload.RetentionDays
	}
	if payload.AlertEnabled != nil {
		settings.AlertEnabled = *payload.AlertEnabled
	}
	if settings.RetentionDays < 1 || settings.RetentionDays > maxAuditRetentionDays {
		BadRequest(c, fmt.Sprintf("retention_days must be between 1 and %d", maxAuditRetentionDays))
		return
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		InternalError(c)
		return
	}
	if err := h.settings.Set(c.Request.Context(), auditSettingsKey, raw); err != nil {
		InternalError(c)
		return
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "update_audit_settings", "settings", auditSettingsKey, map[string]any{
		"retention_days": settings.RetentionDays,
		"alert_enabled":  settings.AlertEnabled,
	}); err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, settings)
}

// GetPortal handles GET /admin/api/settings/portal.
func (h *SettingsHandler) GetPortal(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	settings, err := portalcfg.Load(c.Request.Context(), h.settings)
	if err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, settings)
}

// PutPortal handles PUT /admin/api/settings/portal.
func (h *SettingsHandler) PutPortal(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	var payload portalcfg.Settings
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Normalize()
	if err := payload.Validate(); err != nil {
		BadRequest(c, err.Error())
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		InternalError(c)
		return
	}
	if err := h.settings.Set(c.Request.Context(), portalcfg.SettingsKey, raw); err != nil {
		InternalError(c)
		return
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "update_portal_settings", "settings", portalcfg.SettingsKey, map[string]any{
		"enabled":              payload.Enabled,
		"default_quota":        payload.DefaultQuota,
		"max_request_total":    payload.MaxRequestTotal,
		"request_ttl_days":     payload.RequestTTLDays,
		"claim_ttl_hours":      payload.ClaimTTLHours,
		"usage_max_days":       payload.UsageMaxDays,
		"self_service_enabled": payload.SelfServiceEnabled,
		"max_active_tokens":    payload.MaxActiveTokens,
		"allow_self_rotate":    payload.AllowSelfRotate,
		"allow_self_revoke":    payload.AllowSelfRevoke,
		"auto_approve_quota":   payload.AutoApproveQuota,
	}); err != nil {
		InternalError(c)
		return
	}
	if h.portalInvalidate != nil {
		h.portalInvalidate()
	}
	OK(c, http.StatusOK, payload)
}
