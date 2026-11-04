-- +goose Up
-- Foreign keys do not create indexes in PostgreSQL; department member counts
-- and the employee list filter both scan employees by department_id.
CREATE INDEX IF NOT EXISTS idx_employees_department
    ON employees (department_id);

-- +goose Down
DROP INDEX IF EXISTS idx_employees_department;
