package postgres

import (
	"context"

	"github.com/company/api-key/internal/store"
)

// List returns the department directory in name order.
func (s *DepartmentStore) List(ctx context.Context) ([]store.Department, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM departments ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []store.Department{}
	for rows.Next() {
		var d store.Department
		if err := rows.Scan(&d.ID, &d.Name); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
