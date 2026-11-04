package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	ListenAddr                  string
	PublicBaseURL               string
	DatabaseURL                 string
	AuditMaintenanceDatabaseURL string
	RedisURL                    string
	MasterKey                   []byte
	MasterKeyPrevious           []byte
	CookieSecure                bool
	TrustedProxies              []string
	AllowPrivateUpstreams       bool
	RequestBodyLimit            int64
	CaptureLimit                int
	AuditRetentionDays          int
	AuditSpoolDir               string
	BootstrapAdminUsername      string
	BootstrapAdminPassword      string
	DefaultReserveTokens        int64
	AdminSessionTTLHours        int
	PortalSessionTTLHours       int
}

// Load reads configuration from the environment and validates required values.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:                  envOr("LISTEN_ADDR", ":8080"),
		PublicBaseURL:               envOr("PUBLIC_BASE_URL", "http://localhost:8080"),
		DatabaseURL:                 os.Getenv("DATABASE_URL"),
		AuditMaintenanceDatabaseURL: os.Getenv("AUDIT_MAINTENANCE_DATABASE_URL"),
		RedisURL:                    envOr("REDIS_URL", "redis://localhost:6379/0"),
		AuditSpoolDir:               envOr("AUDIT_SPOOL_DIR", "./spool"),
		BootstrapAdminUsername:      envOr("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminPassword:      os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	masterKey, err := decodeKey("MASTER_KEY", os.Getenv("MASTER_KEY"))
	if err != nil {
		return nil, err
	}
	cfg.MasterKey = masterKey

	if raw := os.Getenv("MASTER_KEY_PREVIOUS"); raw != "" {
		prev, err := decodeKey("MASTER_KEY_PREVIOUS", raw)
		if err != nil {
			return nil, err
		}
		cfg.MasterKeyPrevious = prev
	}

	cfg.CookieSecure = envBool("COOKIE_SECURE", true)
	cfg.TrustedProxies = splitList(os.Getenv("TRUSTED_PROXIES"))
	cfg.AllowPrivateUpstreams = envBool("ALLOW_PRIVATE_UPSTREAMS", false)
	cfg.RequestBodyLimit = envInt64("REQUEST_BODY_LIMIT_BYTES", 2*1024*1024)
	cfg.CaptureLimit = int(envInt64("CAPTURE_LIMIT_BYTES", 256*1024))
	cfg.AuditRetentionDays = int(envInt64("AUDIT_RETENTION_DAYS", 180))
	cfg.DefaultReserveTokens = envInt64("DEFAULT_RESERVE_TOKENS", 4096)
	cfg.AdminSessionTTLHours = int(envInt64("ADMIN_SESSION_TTL_HOURS", 12))
	cfg.PortalSessionTTLHours = int(envInt64("PORTAL_SESSION_TTL_HOURS", 8))

	return cfg, nil
}

func decodeKey(name, raw string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64 encoded: %w", name, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to exactly 32 bytes, got %d", name, len(key))
	}
	return key, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envInt64(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
