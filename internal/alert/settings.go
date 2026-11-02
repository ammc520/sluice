package alert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

// ErrSMTPNotConfigured means the settings table has no smtp entry yet.
var ErrSMTPNotConfigured = errors.New("alert: SMTP is not configured")

const (
	smtpSettingsKey = "smtp"
	smtpPasswordAAD = "setting:smtp.password"
)

type smtpSettings struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	PasswordEnc string   `json:"password_enc"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	UseTLS      bool     `json:"use_tls"`
}

// LoadSMTP reads and decrypts the SMTP settings. Call it before every send so
// administrative changes apply immediately and the plaintext password is not
// kept beyond that send.
func LoadSMTP(ctx context.Context, settings store.SettingsStore, cipher *crypto.Cipher) (SMTPConfig, error) {
	if settings == nil {
		return SMTPConfig{}, errors.New("alert: settings store is required")
	}
	raw, err := settings.Get(ctx, smtpSettingsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SMTPConfig{}, ErrSMTPNotConfigured
		}
		return SMTPConfig{}, fmt.Errorf("alert: load SMTP settings: %w", err)
	}

	var saved smtpSettings
	if err := json.Unmarshal(raw, &saved); err != nil {
		return SMTPConfig{}, errors.New("alert: SMTP settings are invalid")
	}
	cfg := SMTPConfig{
		Host:     saved.Host,
		Port:     saved.Port,
		Username: saved.Username,
		From:     saved.From,
		To:       append([]string(nil), saved.To...),
		UseTLS:   saved.UseTLS,
	}
	if saved.PasswordEnc != "" {
		if cipher == nil {
			return SMTPConfig{}, errors.New("alert: cipher is required to decrypt SMTP password")
		}
		sealed, err := base64.StdEncoding.DecodeString(saved.PasswordEnc)
		if err != nil {
			return SMTPConfig{}, errors.New("alert: SMTP password encoding is invalid")
		}
		plain, err := cipher.Open(sealed, smtpPasswordAAD)
		if err != nil {
			return SMTPConfig{}, errors.New("alert: SMTP password decryption failed")
		}
		cfg.Password = string(plain)
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return cfg, nil
}
