package postgres

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

const employeeColumns = `e.id, e.employee_no, e.name, coalesce(e.email,''), e.department_id,
	coalesce(d.name,''), e.status, e.created_at, e.updated_at,
	q.quota_type, q.quota_period, q.quota_total, coalesce(q.quota_used,0), q.period_start`
const employeeFrom = ` FROM employees e
	LEFT JOIN departments d ON d.id = e.department_id
	LEFT JOIN employee_quotas q ON q.employee_id = e.id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEmployee(row rowScanner) (*store.Employee, error) {
	var e store.Employee
	err := row.Scan(&e.ID, &e.EmployeeNo, &e.Name, &e.Email, &e.DepartmentID,
		&e.DepartmentName, &e.Status, &e.CreatedAt, &e.UpdatedAt,
		&e.QuotaType, &e.QuotaPeriod, &e.QuotaTotal, &e.QuotaUsed, &e.QuotaPeriodStart)
	if err != nil {
		return nil, mapError(err)
	}
	return &e, nil
}

func (s *EmployeeStore) Create(ctx context.Context, e *store.Employee) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO employees (id, employee_no, name, email, department_id, status)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6)
		RETURNING created_at, updated_at`,
		e.ID, e.EmployeeNo, e.Name, e.Email, e.DepartmentID, e.Status,
	).Scan(&e.CreatedAt, &e.UpdatedAt))
}

func (s *EmployeeStore) Get(ctx context.Context, id uuid.UUID) (*store.Employee, error) {
	return scanEmployee(s.pool.QueryRow(ctx, `SELECT `+employeeColumns+employeeFrom+` WHERE e.id=$1`, id))
}

func (s *EmployeeStore) Update(ctx context.Context, e *store.Employee) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE employees SET employee_no=$2, name=$3, email=NULLIF($4,''),
			department_id=$5, status=$6, updated_at=now()
		WHERE id=$1`,
		e.ID, e.EmployeeNo, e.Name, e.Email, e.DepartmentID, e.Status)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *EmployeeStore) List(ctx context.Context, f store.EmployeeFilter) ([]store.Employee, int, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	where := "WHERE 1=1"
	args := []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+q+"%")
		where += " AND (e.employee_no ILIKE $1 OR e.name ILIKE $1 OR e.email ILIKE $1)"
	}
	if f.DepartmentID != nil {
		args = append(args, *f.DepartmentID)
		where += " AND e.department_id = $" + itoa(len(args))
	}
	if f.Unassigned {
		where += " AND e.department_id IS NULL"
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		where += " AND e.status = $" + itoa(len(args))
	}

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*)"+employeeFrom+" "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `SELECT `+employeeColumns+employeeFrom+" "+where+
		" ORDER BY e.created_at DESC LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []store.Employee{}
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}
