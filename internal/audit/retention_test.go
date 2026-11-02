package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retentionFake struct {
	deletedBefore time.Time
	deleted       int64
	err           error
	calls         int
}

func (s *retentionFake) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	s.calls++
	s.deletedBefore = before
	if s.err != nil {
		return 0, s.err
	}
	return s.deleted, nil
}

func TestRetentionDeletesOnlyExpired(t *testing.T) {
	now := time.Date(2026, 11, 2, 3, 0, 0, 0, shanghaiLocation())
	sink := &retentionFake{deleted: 7}
	r := NewRetention(sink, 180, func() time.Time { return now })

	deleted, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 7 {
		t.Fatalf("deleted %d", deleted)
	}
	want := time.Date(2026, 5, 6, 3, 0, 0, 0, shanghaiLocation())
	if !sink.deletedBefore.Equal(want) {
		t.Fatalf("cutoff %v want %v", sink.deletedBefore, want)
	}
}

func TestRetentionRejectsInvalidConfiguration(t *testing.T) {
	r := NewRetention(&retentionFake{}, 0, nil)
	if _, err := r.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error for zero retention days")
	}

	r = NewRetention(nil, 180, nil)
	if _, err := r.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestRetentionReturnsStoreError(t *testing.T) {
	sink := &retentionFake{err: errors.New("delete failed")}
	r := NewRetention(sink, 180, func() time.Time {
		return time.Date(2026, 11, 2, 3, 0, 0, 0, shanghaiLocation())
	})
	if _, err := r.RunOnce(context.Background()); err == nil {
		t.Fatal("expected store error")
	}
}

func TestNextRetentionRunUsesShanghaiThreeAM(t *testing.T) {
	loc := shanghaiLocation()
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before 03:00",
			now:  time.Date(2026, 11, 2, 2, 30, 0, 0, loc),
			want: time.Date(2026, 11, 2, 3, 0, 0, 0, loc),
		},
		{
			name: "at 03:00",
			now:  time.Date(2026, 11, 2, 3, 0, 0, 0, loc),
			want: time.Date(2026, 11, 3, 3, 0, 0, 0, loc),
		},
		{
			name: "after 03:00",
			now:  time.Date(2026, 11, 2, 18, 0, 0, 0, loc),
			want: time.Date(2026, 11, 3, 3, 0, 0, 0, loc),
		},
		{
			name: "utc input",
			now:  time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 11, 3, 3, 0, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextRetentionRun(tc.now); !got.Equal(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
