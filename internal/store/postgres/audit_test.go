package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/store"
)

func TestAuditInsertBatchListAndDeleteBefore(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	recs := []*store.AuditRecord{
		{RequestID: "r1", EventTime: now, Method: "POST", Path: "/v1/chat/completions", StatusCode: 200, DLPAction: "none", ClientIP: "203.0.113.5"},
		{RequestID: "r2", EventTime: now.Add(time.Second), Method: "POST", Path: "/v1/messages", StatusCode: 403, DLPAction: "block", ClientIP: "203.0.113.6"},
	}
	if err := s.Audit.InsertBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Audit.List(ctx, store.AuditFilter{Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if rows[0].RequestID != "r2" {
		t.Fatalf("wrong order: %+v", rows[0])
	}
	if rows[0].ClientIP != "203.0.113.6" {
		t.Fatalf("client ip %q", rows[0].ClientIP)
	}
	n, err := s.Audit.DeleteBefore(ctx, now.Add(time.Minute))
	if err != nil || n != 2 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
}

func TestAuditInsertBatchDeduplicatesRequestID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	rec := &store.AuditRecord{RequestID: "dup", EventTime: now, Method: "POST", Path: "/v1/x", StatusCode: 200, DLPAction: "none"}
	if err := s.Audit.InsertBatch(ctx, []*store.AuditRecord{rec}); err != nil {
		t.Fatal(err)
	}
	if err := s.Audit.InsertBatch(ctx, []*store.AuditRecord{rec}); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Audit.List(ctx, store.AuditFilter{Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
}

func TestAuditFiltersAndCursor(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		rec := &store.AuditRecord{
			RequestID: uuid.NewString(), EventTime: base.Add(time.Duration(i) * time.Minute),
			EmployeeID: &e.ID, Method: "POST", Path: "/v1/chat/completions", StatusCode: 200,
			DLPAction: "none", Model: "gpt-4o", TotalTokens: 10,
		}
		if i == 2 {
			rec.DLPAction = "mask"
			rec.StatusCode = 403
		}
		if err := s.Audit.InsertBatch(ctx, []*store.AuditRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.Audit.List(ctx, store.AuditFilter{EmployeeID: &e.ID, Limit: 2})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	cursor := CursorFor(rows[len(rows)-1].EventTime, rows[len(rows)-1].ID)
	next, err := s.Audit.List(ctx, store.AuditFilter{Cursor: cursor, Limit: 2})
	if err != nil || len(next) != 1 {
		t.Fatalf("next=%d err=%v", len(next), err)
	}

	block := "mask"
	rows, _ = s.Audit.List(ctx, store.AuditFilter{DLPAction: block})
	if len(rows) != 1 {
		t.Fatalf("got %d", len(rows))
	}
	code := 403
	rows, _ = s.Audit.List(ctx, store.AuditFilter{StatusCode: &code})
	if len(rows) != 1 {
		t.Fatalf("got %d", len(rows))
	}
}

func TestAuditDashboardAggregates(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	now := time.Now().UTC()
	hits := []byte(`[{"rule_name":"phone","action":"mask"}]`)
	recs := []*store.AuditRecord{
		{RequestID: uuid.NewString(), EventTime: now, EmployeeID: &e.ID, Method: "POST", Path: "/v1/x", StatusCode: 200, DLPAction: "mask", TotalTokens: 10, DLPHits: hits},
		{RequestID: uuid.NewString(), EventTime: now, EmployeeID: &e.ID, Method: "POST", Path: "/v1/x", StatusCode: 500, DLPAction: "none", TotalTokens: 5},
	}
	if err := s.Audit.InsertBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	o, err := s.Audit.Overview(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if o.TotalCalls != 2 || o.ActiveUsers != 1 || o.TotalTokens != 15 || o.DLPHits != 1 || o.ErrorCalls != 1 {
		t.Fatalf("overview %+v", o)
	}
	trend, err := s.Audit.Trend(ctx, from, to)
	if err != nil || len(trend) != 1 || trend[0].Calls != 2 {
		t.Fatalf("trend %+v err=%v", trend, err)
	}
	top, err := s.Audit.TopUsers(ctx, from, to, 10)
	if err != nil || len(top) != 1 || top[0].Tokens != 15 {
		t.Fatalf("top %+v err=%v", top, err)
	}
	stats, err := s.Audit.DLPStats(ctx, from, to)
	if err != nil || len(stats) != 1 || stats[0].RuleName != "phone" || stats[0].Hits != 1 {
		t.Fatalf("stats %+v err=%v", stats, err)
	}
}
