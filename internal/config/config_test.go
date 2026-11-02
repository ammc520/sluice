package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadRejectsMissingMasterKey(t *testing.T) {
	t.Setenv("MASTER_KEY", "")
	t.Setenv("DATABASE_URL", "postgres://x")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MASTER_KEY") {
		t.Fatalf("expected MASTER_KEY error, got %v", err)
	}
}

func TestLoadDecodes32ByteMasterKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	t.Setenv("MASTER_KEY", key)
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MasterKey) != 32 {
		t.Fatalf("got %d bytes", len(cfg.MasterKey))
	}
}

func TestLoadRejectsWrongMasterKeyLength(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	t.Setenv("MASTER_KEY", key)
	t.Setenv("DATABASE_URL", "postgres://x")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadDefaults(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	t.Setenv("MASTER_KEY", key)
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.RequestBodyLimit != 2097152 ||
		cfg.CaptureLimit != 262144 || cfg.AuditRetentionDays != 180 || !cfg.CookieSecure {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}
