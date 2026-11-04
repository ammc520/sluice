package postgres

import (
	"context"

	"github.com/company/sluice/internal/store"
)

// Directory returns departments with their member counts plus org-wide
// totals for the "all employees" and "unassigned" buckets.
func (s *DepartmentStore) Directory(ctx context.Context) (store.DepartmentDirectory, error) {
	out := store.DepartmentDirectory{Items: []store.Department{}}

	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.name, count(e.id)
		FROM departments d
		LEFT JOIN employees e ON e.department_id = d.id
		GROUP BY d.id, d.name
		ORDER BY d.name`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d store.Department
		if err := rows.Scan(&d.ID, &d.Name, &d.EmployeeCount); err != nil {
			return out, err
		}
		out.Items = append(out.Items, d)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE department_id IS NULL), count(*)
		FROM employees`).Scan(&out.Unassigned, &out.Total); err != nil {
		return out, err
	}
	return out, nil
}

// Create inserts a department and returns it with a zero member count.
func (s *DepartmentStore) Create(ctx context.Context, name string) (*store.Department, error) {
	d := store.Department{}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO departments (name) VALUES ($1) RETURNING id, name`, name,
	).Scan(&d.ID, &d.Name); err != nil {
		return nil, mapError(err)
	}
	return &d, nil
}

// Rename updates a department's name.
func (s *DepartmentStore) Rename(ctx context.Context, id int64, name string) (*store.Department, error) {
	d := store.Department{}
	if err := s.pool.QueryRow(ctx,
		`UPDATE departments SET name=$2 WHERE id=$1 RETURNING id, name`, id, name,
	).Scan(&d.ID, &d.Name); err != nil {
		return nil, mapError(err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM employees WHERE department_id=$1`, id,
	).Scan(&d.EmployeeCount); err != nil {
		return nil, err
	}
	return &d, nil
}

// Delete removes a department. When opts.Reassign is set, its employees are
// first moved to opts.MoveTo (or unassigned when MoveTo is nil); otherwise the
// delete fails with ErrConflict if the department still has members.
func (s *DepartmentStore) Delete(ctx context.Context, id int64, opts store.DepartmentDeleteOptions) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var moved int64
	if opts.Reassign {
		if opts.MoveTo != nil {
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1)`, *opts.MoveTo,
			).Scan(&exists); err != nil {
				return 0, err
			}
			if !exists {
				return 0, store.ErrInvalidReference
			}
		}
		tag, err := tx.Exec(ctx,
			`UPDATE employees SET department_id=$2, updated_at=now() WHERE department_id=$1`,
			id, opts.MoveTo)
		if err != nil {
			return 0, mapError(err)
		}
		moved = tag.RowsAffected()
	} else {
		var count int64
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM employees WHERE department_id=$1`, id,
		).Scan(&count); err != nil {
			return 0, err
		}
		if count > 0 {
			return 0, store.ErrConflict
		}
	}

	tag, err := tx.Exec(ctx, `DELETE FROM departments WHERE id=$1`, id)
	if err != nil {
		return 0, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return 0, store.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return moved, nil
}
