package memory

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

func TestListApprovalsByRequestFiltersAtTheStore(t *testing.T) {
	s := New()
	ctx := context.Background()
	for _, tk := range []domain.Task{{ID: "t1", RequestID: "r1"}, {ID: "t2", RequestID: "r2"}} {
		if err := s.CreateTask(ctx, "o", tk); err != nil {
			t.Fatal(err)
		}
	}
	for i, tid := range []string{"t1", "t2", "project:p1", "other"} {
		if err := s.CreateApproval(ctx, "o", domain.Approval{ID: "a" + string(rune('0'+i)), TaskID: tid, Status: domain.ApprovalPending, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListApprovalsByRequest(ctx, "o", "r1", []string{"project:p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TaskID != "project:p1" || got[1].TaskID != "t1" {
		t.Fatalf("want [project:p1 t1] (newest first), got %+v", got)
	}
}
