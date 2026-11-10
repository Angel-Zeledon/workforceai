package projects_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// officeRule (D3) is an approver policy where every approval belongs to an office whose only responsible is "lead".
type officeRule struct{}

func (officeRule) CanDecide(_ context.Context, _, user string, _ domain.Approval) (bool, error) {
	return user == "lead", nil
}
func (officeRule) Sees(context.Context, string, string, domain.Approval) bool { return true }
func (officeRule) Approvers(context.Context, string, domain.Approval) []string {
	return []string{"lead"}
}

// D3: cancelling a project withdraws its pending approvals as the system (not as a decision of the person who
// cancels, who may not be their office's responsible), and the batch only decides what the person may see.
func TestCancelWithdrawsAndBatchSkipsHiddenUnderTheOfficeRule(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	e.appr.SetPolicy(officeRule{})
	id := e.draft(tFC, 0)
	e.launch(id, budgetBig)
	pending := func(action string) []projects.Approval {
		var out []projects.Approval
		for _, a := range e.detail(id).Approvals {
			if a.Action == action && a.Status == "pending" {
				out = append(out, a)
			}
		}
		return out
	}
	e.waitFor("two publish_statement approvals", func() bool { return len(pending("publish_statement")) == 2 })

	// the batch of the responsible, seeing only one of the two: only that one is counted and decided
	hidden := pending("publish_statement")[0].ID
	lead := application.WithActor(e.ctx, "lead")
	batch := projects.BatchDecision{Decision: "approve", ExpectedCount: 1, Visible: func(a domain.Approval) bool { return a.ID != hidden }}
	batch.Filter.ProjectID, batch.Filter.Action = id, "publish_statement"
	if res, err := e.svc.DecideBatch(lead, batch); err != nil || res.Decided != 1 {
		t.Fatalf("batch: %v %+v", err, res)
	}
	if left := pending("publish_statement"); len(left) != 1 || left[0].ID != hidden {
		t.Fatalf("left pending: %+v", left)
	}

	// someone who is not the responsible cancels: the approval is withdrawn all the same
	if _, err := e.svc.Control(application.WithActor(e.ctx, "boss"), id, "cancel"); err != nil {
		t.Fatal(err)
	}
	if ap, err := e.store.GetApproval(e.ctx, application.DefaultConfig().OrgID, hidden); err != nil || ap.Status != domain.ApprovalRejected {
		t.Fatalf("after the cancel: %+v %v", ap.Status, err)
	}
}
