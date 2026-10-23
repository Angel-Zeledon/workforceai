package quality_test

import (
	"context"
	"fmt"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Changes (the quality_drop signal of proactive employees) only reports agents scored in both windows.
func TestChangesOnlyComparesScoredWindows(t *testing.T) {
	e := newEnv(t)
	ctx := application.WithOrg(context.Background(), "org-a")
	for i := 0; i < 12; i++ { // previous window: all approved
		e.approval(t, fmt.Sprintf("p-ana-%d", i), "ana", domain.ApprovalApproved, "", 40*day)
	}
	for i := 0; i < 12; i++ { // current window: half rejected
		st := domain.ApprovalApproved
		if i%2 == 0 {
			st = domain.ApprovalRejected
		}
		e.approval(t, fmt.Sprintf("c-ana-%d", i), "ana", st, "no", day)
		e.approval(t, fmt.Sprintf("c-bob-%d", i), "bob", domain.ApprovalRejected, "no", day) // no previous window
	}
	got, err := e.svc.Changes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentID != "ana" || got[0].Previous != 100 || got[0].Score != 50 {
		t.Fatalf("changes = %+v", got)
	}
}
