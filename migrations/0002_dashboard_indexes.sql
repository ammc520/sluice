-- +goose Up
CREATE INDEX IF NOT EXISTS idx_audit_provider_time
    ON audit_logs (provider_id, event_time DESC);

CREATE INDEX IF NOT EXISTS idx_audit_department_time
    ON audit_logs (department_id, event_time DESC);

CREATE INDEX IF NOT EXISTS idx_audit_dlp_window
    ON audit_logs (event_time DESC)
    WHERE dlp_action <> 'none';

-- +goose Down
DROP INDEX IF EXISTS idx_audit_dlp_window;
DROP INDEX IF EXISTS idx_audit_department_time;
DROP INDEX IF EXISTS idx_audit_provider_time;
