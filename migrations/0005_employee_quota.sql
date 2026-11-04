-- +goose Up
-- Employee-level quota pool. Keys become credentials: every key an employee
-- holds shares the same quota_used counter, while a key may still carry an
-- optional per-key cap in tokens.quota_total.

CREATE TABLE employee_quotas (
    employee_id  UUID PRIMARY KEY REFERENCES employees(id) ON DELETE CASCADE,
    quota_type   TEXT NOT NULL DEFAULT 'tokens'
                 CHECK (quota_type IN ('requests','tokens')),
    quota_period TEXT NOT NULL DEFAULT 'monthly'
                 CHECK (quota_period IN ('daily','monthly','total')),
    quota_total  BIGINT NOT NULL DEFAULT 1000000,
    quota_used   BIGINT NOT NULL DEFAULT 0,
    period_start TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by   UUID REFERENCES admin_users(id),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Existing tokens were the quota subject. Their quota_total is retained as an
-- optional per-key cap; the employee row becomes the shared pool. The backfill
-- is deliberately conservative: the pool is at least the default allowance and
-- at least the sum of the caps, while quota_used starts from the largest key
-- counter so nobody is granted a fresh allowance on migration day.
INSERT INTO employee_quotas (employee_id, quota_type, quota_period, quota_total,
    quota_used, period_start)
SELECT e.id,
       'tokens',
       'monthly',
       GREATEST(1000000::bigint,
           COALESCE((SELECT sum(t.quota_total) FROM tokens t
                     WHERE t.employee_id = e.id AND t.status = 1
                       AND t.quota_total IS NOT NULL), 0)),
       COALESCE((SELECT max(t.quota_used) FROM tokens t
                 WHERE t.employee_id = e.id), 0),
       date_trunc('month', now() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'
FROM employees e;

ALTER TABLE tokens ADD COLUMN name TEXT NOT NULL DEFAULT '';

ALTER TABLE token_requests ADD COLUMN kind TEXT NOT NULL DEFAULT 'token'
    CHECK (kind IN ('quota','token'));
-- New requests are quota-increase requests. Rows that existed before this
-- migration keep kind='token' so their claim flow remains available.
ALTER TABLE token_requests ALTER COLUMN kind SET DEFAULT 'quota';

ALTER TABLE token_requests DROP CONSTRAINT token_requests_status_check;
ALTER TABLE token_requests ADD CONSTRAINT token_requests_status_check
    CHECK (status IN ('pending','approved','granted','denied','cancelled','claimed','expired'));

CREATE INDEX idx_employee_quotas_pressure ON employee_quotas (quota_used DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_employee_quotas_pressure;
ALTER TABLE token_requests DROP CONSTRAINT token_requests_status_check;
UPDATE token_requests SET status='approved' WHERE status='granted';
ALTER TABLE token_requests ADD CONSTRAINT token_requests_status_check
    CHECK (status IN ('pending','approved','denied','cancelled','claimed','expired'));
ALTER TABLE token_requests DROP COLUMN IF EXISTS kind;
ALTER TABLE tokens DROP COLUMN IF EXISTS name;
DROP TABLE IF EXISTS employee_quotas;
