-- +goose Up
-- Employee self-service portal: credentials, sessions, token requests, and
-- self-service action audit. Token plaintext is never stored here; the token
-- itself lives in tokens as a SHA-256 hash.

CREATE TABLE employee_accounts (
    employee_id          UUID PRIMARY KEY REFERENCES employees(id) ON DELETE CASCADE,
    password_hash        TEXT NOT NULL,
    must_change_password BOOLEAN NOT NULL DEFAULT true,
    failed_attempts      INTEGER NOT NULL DEFAULT 0,
    locked_until         TIMESTAMPTZ,
    last_login_at        TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employee_sessions (
    id           UUID PRIMARY KEY,
    employee_id  UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    token_hash   CHAR(64) NOT NULL UNIQUE,
    csrf_hash    CHAR(64) NOT NULL,
    ip           INET,
    user_agent   TEXT NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_employee_sessions_employee ON employee_sessions (employee_id);
CREATE INDEX idx_employee_sessions_expires ON employee_sessions (expires_at);

CREATE TABLE token_requests (
    id                   UUID PRIMARY KEY,
    employee_id          UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    reason               TEXT NOT NULL,
    requested_total      BIGINT,
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending','approved','denied','cancelled','claimed','expired')),
    reviewed_by          UUID REFERENCES admin_users(id),
    review_note          TEXT NOT NULL DEFAULT '',
    reviewed_at          TIMESTAMPTZ,
    granted_quota_type   TEXT,
    granted_quota_period TEXT,
    granted_total        BIGINT,
    granted_rpm          INTEGER,
    granted_expires_at   TIMESTAMPTZ,
    token_id             UUID REFERENCES tokens(id),
    expires_at           TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_token_requests_employee ON token_requests (employee_id, created_at DESC);
CREATE INDEX idx_token_requests_status ON token_requests (status, created_at DESC);

-- At most one live request per employee. Expired requests are swept before
-- a new one is inserted, so this index never blocks a legitimate retry.
CREATE UNIQUE INDEX idx_token_requests_active
    ON token_requests (employee_id) WHERE status IN ('pending','approved');

CREATE TABLE employee_audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    action      VARCHAR(64) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    target_id   TEXT,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    client_ip   INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_employee_audit_employee ON employee_audit_logs (employee_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS employee_audit_logs;
DROP TABLE IF EXISTS token_requests;
DROP TABLE IF EXISTS employee_sessions;
DROP TABLE IF EXISTS employee_accounts;
