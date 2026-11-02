package postgres

import (
	"errors"
	"testing"

	"github.com/company/api-key/internal/store"
)

func TestEmployeeCreateGetUpdateList(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)

	got, err := s.Employees.Get(ctx, e.ID)
	if err != nil || got.EmployeeNo != e.EmployeeNo {
		t.Fatalf("got=%+v err=%v", got, err)
	}

	got.Name = "Alice Updated"
	got.Status = 0
	if err := s.Employees.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Employees.Get(ctx, e.ID)
	if again.Name != "Alice Updated" || again.Status != 0 {
		t.Fatalf("got %+v", again)
	}

	list, total, err := s.Employees.List(ctx, store.EmployeeFilter{Query: "Updated"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("total=%d list=%d err=%v", total, len(list), err)
	}
}

func TestEmployeeUpdateMissingReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	e := seedEmployee(t, s)
	e.ID = uuidMust()
	if err := s.Employees.Update(ctx, e); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
