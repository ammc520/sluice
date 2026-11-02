package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

const adminUserColumns = `id, username, password_hash, role, status, last_login_at, created_at`

func scanAdminUser(row rowScanner) (*store.AdminUser, error) {
	var u store.AdminUser
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status, &u.LastLoginAt, &u.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &u, nil
}

func (s *AdminStore) FindByUsername(ctx context.Context, username string) (*store.AdminUser, error) {
	return scanAdminUser(s.pool.QueryRow(ctx, `SELECT `+adminUserColumns+` FROM admin_users WHERE username=$1`, username))
}

func (s *AdminStore) Get(ctx context.Context, id uuid.UUID) (*store.AdminUser, error) {
	return scanAdminUser(s.pool.QueryRow(ctx, `SELECT `+adminUserColumns+` FROM admin_users WHERE id=$1`, id))
}

func (s *AdminStore) Create(ctx context.Context, u *store.AdminUser) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO admin_users (id, username, password_hash, role, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.Status,
	).Scan(&u.CreatedAt))
}

func (s *AdminStore) Update(ctx context.Context, u *store.AdminUser) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize administrator edits so two concurrent requests cannot each
	// observe another active super administrator and both remove one.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(68421001)`); err != nil {
		return err
	}

	var oldRole string
	var oldStatus int16
	if err := tx.QueryRow(ctx,
		`SELECT role, status FROM admin_users WHERE id=$1 FOR UPDATE`, u.ID,
	).Scan(&oldRole, &oldStatus); err != nil {
		return mapError(err)
	}

	if oldRole == "super_admin" && oldStatus == 1 &&
		(u.Role != "super_admin" || u.Status != 1) {
		var active int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM admin_users WHERE role='super_admin' AND status=1`,
		).Scan(&active); err != nil {
			return err
		}
		if active <= 1 {
			return store.ErrLastSuperAdmin
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE admin_users SET username=$2, password_hash=$3, role=$4, status=$5,
			last_login_at=$6 WHERE id=$1`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.Status, u.LastLoginAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *AdminStore) List(ctx context.Context) ([]store.AdminUser, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+adminUserColumns+` FROM admin_users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.AdminUser{}
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

const adminSessionColumns = `id, admin_id, token_hash, csrf_hash, expires_at, created_at, last_seen_at`

func scanAdminSession(row rowScanner) (*store.AdminSession, error) {
	var s store.AdminSession
	err := row.Scan(&s.ID, &s.AdminID, &s.TokenHash, &s.CSRFHash, &s.ExpiresAt, &s.CreatedAt, &s.LastSeenAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &s, nil
}

func (s *AdminStore) CreateSession(ctx context.Context, sess *store.AdminSession) error {
	if sess.ID == uuid.Nil {
		sess.ID = uuid.New()
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO admin_sessions (id, admin_id, token_hash, csrf_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5) RETURNING created_at, last_seen_at`,
		sess.ID, sess.AdminID, sess.TokenHash, sess.CSRFHash, sess.ExpiresAt,
	).Scan(&sess.CreatedAt, &sess.LastSeenAt))
}

func (s *AdminStore) FindSessionByHash(ctx context.Context, hash string) (*store.AdminSession, error) {
	return scanAdminSession(s.pool.QueryRow(ctx, `SELECT `+adminSessionColumns+` FROM admin_sessions WHERE token_hash=$1`, hash))
}

func (s *AdminStore) TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_sessions SET last_seen_at=$2 WHERE id=$1`, id, at)
	return err
}

func (s *AdminStore) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash=$1`, hash)
	return err
}

func (s *AdminStore) DeleteSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_id=$1`, adminID)
	return err
}

func (s *AdminStore) DeleteExpiredSessions(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < $1`, before)
	return err
}

func (s *AdminStore) InsertAdminAudit(ctx context.Context, a *store.AdminAuditLog) error {
	detail := a.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	return mapError(s.pool.QueryRow(ctx, `
		INSERT INTO admin_audit_logs (admin_id, action, target_type, target_id, detail, client_ip)
		VALUES ($1,$2,$3,$4,$5::jsonb,NULLIF($6,'')::inet) RETURNING id, created_at`,
		a.AdminID, a.Action, a.TargetType, a.TargetID, detail, a.ClientIP,
	).Scan(&a.ID, &a.CreatedAt))
}
