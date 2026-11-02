-- +goose Up
CREATE TABLE departments (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employees (
    id            UUID PRIMARY KEY,
    employee_no   VARCHAR(64) NOT NULL UNIQUE,
    name          VARCHAR(128) NOT NULL,
    email         VARCHAR(255),
    department_id BIGINT REFERENCES departments(id),
    status        SMALLINT NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tokens (
    id             UUID PRIMARY KEY,
    employee_id    UUID NOT NULL REFERENCES employees(id),
    token_hash     CHAR(64) NOT NULL UNIQUE,
    token_prefix   VARCHAR(16) NOT NULL,
    token_last4    VARCHAR(4) NOT NULL,
    quota_type     TEXT NOT NULL DEFAULT 'tokens'
                   CHECK (quota_type IN ('requests','tokens')),
    quota_period   TEXT NOT NULL DEFAULT 'monthly'
                   CHECK (quota_period IN ('daily','monthly','total')),
    quota_total    BIGINT,
    quota_used     BIGINT NOT NULL DEFAULT 0,
    period_start   TIMESTAMPTZ,
    rate_limit_rpm INTEGER NOT NULL DEFAULT 60,
    ip_allowlist   JSONB NOT NULL DEFAULT '[]'::jsonb,
    status         SMALLINT NOT NULL DEFAULT 1,
    expires_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ
);

CREATE INDEX idx_tokens_employee ON tokens (employee_id);

CREATE TABLE upstream_providers (
    id              UUID PRIMARY KEY,
    name            VARCHAR(128) NOT NULL UNIQUE,
    protocol        TEXT NOT NULL
                    CHECK (protocol IN ('openai_compatible','anthropic')),
    base_url        TEXT NOT NULL,
    api_key_enc     BYTEA NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_routes (
    id             UUID PRIMARY KEY,
    provider_id    UUID NOT NULL REFERENCES upstream_providers(id),
    protocol       TEXT NOT NULL
                   CHECK (protocol IN ('openai_compatible','anthropic')),
    pattern        TEXT NOT NULL,
    match_type     TEXT NOT NULL DEFAULT 'prefix'
                   CHECK (match_type IN ('exact','prefix','glob')),
    upstream_model TEXT,
    priority       INTEGER NOT NULL DEFAULT 100,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_model_routes_priority ON model_routes (enabled, protocol, priority, created_at);

CREATE TABLE dlp_rules (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    category      VARCHAR(64) NOT NULL,
    match_type    TEXT NOT NULL CHECK (match_type IN ('regex','keyword')),
    pattern       TEXT NOT NULL,
    action        TEXT NOT NULL CHECK (action IN ('block','mask','audit')),
    replacement   TEXT NOT NULL DEFAULT '***',
    priority      INTEGER NOT NULL DEFAULT 100,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    builtin       BOOLEAN NOT NULL DEFAULT false,
    alert_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id                BIGSERIAL PRIMARY KEY,
    event_time        TIMESTAMPTZ NOT NULL,
    request_id        VARCHAR(64) NOT NULL UNIQUE,
    token_id          UUID,
    employee_id       UUID,
    department_id     BIGINT,
    client_ip         INET,
    method            VARCHAR(8) NOT NULL,
    path              TEXT NOT NULL,
    model             VARCHAR(128),
    provider_id       UUID,
    status_code       INTEGER NOT NULL,
    error_code        VARCHAR(64),
    latency_ms        INTEGER NOT NULL,
    upstream_latency_ms INTEGER,
    first_byte_ms     INTEGER,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    usage_estimated   BOOLEAN NOT NULL DEFAULT false,
    prompt_enc        BYTEA,
    response_enc      BYTEA,
    prompt_truncated  BOOLEAN NOT NULL DEFAULT false,
    response_truncated BOOLEAN NOT NULL DEFAULT false,
    dlp_action        TEXT NOT NULL DEFAULT 'none'
                      CHECK (dlp_action IN ('none','block','mask','audit')),
    dlp_hits          JSONB NOT NULL DEFAULT '[]'::jsonb,
    client_disconnected BOOLEAN NOT NULL DEFAULT false,
    upstream_request_id TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_event_time ON audit_logs (event_time DESC);
CREATE INDEX idx_audit_employee_time ON audit_logs (employee_id, event_time DESC);
CREATE INDEX idx_audit_token_time ON audit_logs (token_id, event_time DESC);
CREATE INDEX idx_audit_dlp_time ON audit_logs (dlp_action, event_time DESC);
CREATE INDEX idx_audit_status_time ON audit_logs (status_code, event_time DESC);

CREATE TABLE admin_users (
    id            UUID PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL
                  CHECK (role IN ('super_admin','operator','auditor')),
    status        SMALLINT NOT NULL DEFAULT 1,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_sessions (
    id          UUID PRIMARY KEY,
    admin_id    UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash  CHAR(64) NOT NULL UNIQUE,
    csrf_hash   CHAR(64) NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_admin_sessions_expires ON admin_sessions (expires_at);

CREATE TABLE admin_audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    admin_id    UUID NOT NULL,
    action      VARCHAR(64) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    target_id   TEXT,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    client_ip   INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    key        VARCHAR(128) PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS admin_sessions;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS dlp_rules;
DROP TABLE IF EXISTS model_routes;
DROP TABLE IF EXISTS upstream_providers;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS employees;
DROP TABLE IF EXISTS departments;
