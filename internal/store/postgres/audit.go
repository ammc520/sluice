package postgres

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/company/api-key/internal/store"
)

const auditColumns = `id, event_time, request_id, token_id, employee_id, department_id,
	coalesce(host(client_ip),''), method, path, coalesce(model,''), provider_id, status_code,
	coalesce(error_code,''), latency_ms, coalesce(upstream_latency_ms,0), coalesce(first_byte_ms,0),
	prompt_tokens, completion_tokens, total_tokens, usage_estimated, prompt_enc, response_enc,
	prompt_truncated, response_truncated, dlp_action, dlp_hits, client_disconnected,
	coalesce(upstream_request_id,''), created_at`

func scanAudit(row rowScanner) (*store.AuditRecord, error) {
	var a store.AuditRecord
	err := row.Scan(&a.ID, &a.EventTime, &a.RequestID, &a.TokenID, &a.EmployeeID, &a.DepartmentID,
		&a.ClientIP, &a.Method, &a.Path, &a.Model, &a.ProviderID, &a.StatusCode, &a.ErrorCode,
		&a.LatencyMS, &a.UpstreamLatencyMS, &a.FirstByteMS, &a.PromptTokens, &a.CompletionTokens,
		&a.TotalTokens, &a.UsageEstimated, &a.PromptEnc, &a.ResponseEnc, &a.PromptTruncated,
		&a.ResponseTruncated, &a.DLPAction, &a.DLPHits, &a.ClientDisconnected,
		&a.UpstreamRequestID, &a.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &a, nil
}

func (s *AuditStore) InsertBatch(ctx context.Context, records []*store.AuditRecord) error {
	if len(records) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO audit_logs (event_time, request_id, token_id, employee_id,
		department_id, client_ip, method, path, model, provider_id, status_code, error_code,
		latency_ms, upstream_latency_ms, first_byte_ms, prompt_tokens, completion_tokens,
		total_tokens, usage_estimated, prompt_enc, response_enc, prompt_truncated,
		response_truncated, dlp_action, dlp_hits, client_disconnected, upstream_request_id)
		VALUES `)
	args := make([]any, 0, len(records)*27)
	for i, r := range records {
		if i > 0 {
			b.WriteString(",")
		}
		n := len(args)
		b.WriteString("(")
		for j := 1; j <= 27; j++ {
			if j > 1 {
				b.WriteString(",")
			}
			if j == 6 {
				b.WriteString("NULLIF($" + strconv.Itoa(n+j) + ",'')::inet")
			} else if j == 25 {
				b.WriteString("$" + strconv.Itoa(n+j) + "::jsonb")
			} else {
				b.WriteString("$" + strconv.Itoa(n+j))
			}
		}
		b.WriteString(")")
		dlpHits := r.DLPHits
		if len(dlpHits) == 0 {
			dlpHits = []byte("[]")
		}
		args = append(args, r.EventTime, r.RequestID, r.TokenID, r.EmployeeID, r.DepartmentID,
			r.ClientIP, r.Method, r.Path, r.Model, r.ProviderID, r.StatusCode, r.ErrorCode,
			r.LatencyMS, r.UpstreamLatencyMS, r.FirstByteMS, r.PromptTokens, r.CompletionTokens,
			r.TotalTokens, r.UsageEstimated, r.PromptEnc, r.ResponseEnc, r.PromptTruncated,
			r.ResponseTruncated, r.DLPAction, dlpHits, r.ClientDisconnected, r.UpstreamRequestID)
	}
	b.WriteString(" ON CONFLICT (request_id) DO NOTHING")
	_, err := s.pool.Exec(ctx, b.String(), args...)
	return err
}

func (s *AuditStore) List(ctx context.Context, f store.AuditFilter) ([]*store.AuditRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	where := "WHERE 1=1"
	args := []any{}
	add := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.From != nil {
		where += " AND event_time >= " + add(*f.From)
	}
	if f.To != nil {
		where += " AND event_time < " + add(*f.To)
	}
	if f.EmployeeID != nil {
		where += " AND employee_id = " + add(*f.EmployeeID)
	}
	if f.DepartmentID != nil {
		where += " AND department_id = " + add(*f.DepartmentID)
	}
	if f.StatusCode != nil {
		where += " AND status_code = " + add(*f.StatusCode)
	}
	if f.DLPAction != "" {
		where += " AND dlp_action = " + add(f.DLPAction)
	}
	if f.Model != "" {
		where += " AND model = " + add(f.Model)
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		where += " AND (event_time, id) < (" + add(at) + ", " + add(id) + ")"
	}
	q := `SELECT ` + auditColumns + ` FROM audit_logs ` + where +
		" ORDER BY event_time DESC, id DESC LIMIT " + add(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.AuditRecord{}
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *AuditStore) Get(ctx context.Context, id int64) (*store.AuditRecord, error) {
	return scanAudit(s.pool.QueryRow(ctx, `SELECT `+auditColumns+` FROM audit_logs WHERE id=$1`, id))
}

func (s *AuditStore) Overview(ctx context.Context, from, to time.Time) (store.DashboardOverview, error) {
	var o store.DashboardOverview
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
			count(DISTINCT employee_id),
			coalesce(sum(total_tokens),0),
			coalesce(sum(CASE WHEN dlp_action <> 'none' THEN 1 ELSE 0 END),0),
			coalesce(sum(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END),0)
		FROM audit_logs WHERE event_time >= $1 AND event_time < $2`, from, to).
		Scan(&o.TotalCalls, &o.ActiveUsers, &o.TotalTokens, &o.DLPHits, &o.ErrorCalls)
	return o, err
}

func (s *AuditStore) Trend(ctx context.Context, from, to time.Time) ([]store.TrendPoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', event_time AT TIME ZONE 'Asia/Shanghai')::date AS day,
			count(*), coalesce(sum(total_tokens),0)
		FROM audit_logs WHERE event_time >= $1 AND event_time < $2
		GROUP BY day ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TrendPoint{}
	for rows.Next() {
		var p store.TrendPoint
		if err := rows.Scan(&p.Day, &p.Calls, &p.Tokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *AuditStore) TopUsers(ctx context.Context, from, to time.Time, limit int) ([]store.UserUsage, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.employee_id, coalesce(e.name,''), count(*), coalesce(sum(a.total_tokens),0) AS tokens
		FROM audit_logs a LEFT JOIN employees e ON e.id = a.employee_id
		WHERE a.event_time >= $1 AND a.event_time < $2 AND a.employee_id IS NOT NULL
		GROUP BY a.employee_id, e.name ORDER BY tokens DESC LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.UserUsage{}
	for rows.Next() {
		var u store.UserUsage
		if err := rows.Scan(&u.EmployeeID, &u.Name, &u.Calls, &u.Tokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *AuditStore) DLPStats(ctx context.Context, from, to time.Time) ([]store.DLPStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT elem->>'rule_name', elem->>'action', count(*)
		FROM audit_logs, jsonb_array_elements(dlp_hits) elem
		WHERE event_time >= $1 AND event_time < $2
		GROUP BY 1,2 ORDER BY 3 DESC LIMIT 20`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.DLPStat{}
	for rows.Next() {
		var d store.DLPStat
		if err := rows.Scan(&d.RuleName, &d.Action, &d.Hits); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *AuditStore) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_logs WHERE event_time < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func encodeCursor(at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)))
}

func decodeCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor")
	}
	return at, id, nil
}

// CursorFor is exported for the admin API to build the next-page cursor.
func CursorFor(at time.Time, id int64) string { return encodeCursor(at, id) }
