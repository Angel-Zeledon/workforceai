package slackapp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// fakeOffices is the D3 rule of a model with one private office (agent-legal; members legal-lead and legal-member,
// legal-lead its responsible) and one open office (agent-sales; sales-lead its responsible). agent-1 is of General.
type fakeOffices struct{ broken bool }

func (f fakeOffices) office(agent string) (members, leads []string, private, ok bool) {
	switch agent {
	case "agent-legal":
		return []string{"legal-lead", "legal-member"}, []string{"legal-lead"}, true, true
	case "agent-sales":
		return nil, []string{"sales-lead"}, false, true
	}
	return nil, nil, false, false
}

func (f fakeOffices) Sees(_ context.Context, _, userID string, a domain.Approval) bool {
	if f.broken {
		return false
	}
	members, _, private, _ := f.office(a.AgentID)
	return !private || slices.Contains(members, userID) || userID != "" && a.RequestedBy == userID
}

func (f fakeOffices) CanDecide(ctx context.Context, org, userID string, a domain.Approval) (bool, error) {
	if f.broken {
		return false, errors.New("model unavailable")
	}
	if !f.Sees(ctx, org, userID, a) {
		return false, nil
	}
	_, leads, _, ok := f.office(a.AgentID)
	return !ok || len(leads) == 0 || slices.Contains(leads, userID), nil
}

func (f fakeOffices) Approvers(_ context.Context, _ string, a domain.Approval) []string {
	if f.broken {
		return nil
	}
	_, leads, _, _ := f.office(a.AgentID)
	return leads
}

func (f fakeOffices) rule() OfficeRule {
	return OfficeRule{Policy: f, Private: func(_ context.Context, _ string, a domain.Approval) (bool, error) {
		if f.broken {
			return false, errors.New("model unavailable")
		}
		_, _, private, _ := f.office(a.AgentID)
		return private, nil
	}}
}

func (h *harness) offices(f fakeOffices) {
	h.svc.SetOffices(f.rule())
	h.appr.SetPolicy(f)
}

// D3 (I3): an approval of a private office is never posted in the company channel; one of an open office or of
// General is (its buttons are refused to whoever is not its responsible). When the office cannot be told, nothing is
// posted.
func TestPrivateOfficeApprovalsAreNotPostedInTheChannel(t *testing.T) {
	h := newHarness(t)
	h.offices(fakeOffices{})
	h.svc.Post(context.Background(), org, h.approval("ap-legal", func(a *domain.Approval) { a.AgentID = "agent-legal" }))
	if len(h.slack.posts) != 0 {
		t.Fatalf("a private office's approval was posted: %v", h.slack.posts)
	}
	h.svc.Post(context.Background(), org, h.approval("ap-sales", func(a *domain.Approval) { a.AgentID = "agent-sales" }))
	h.svc.Post(context.Background(), org, h.approval("ap-gen", nil))
	if len(h.slack.posts) != 2 {
		t.Fatalf("open office / General approvals posted = %d, want 2", len(h.slack.posts))
	}
	// The observer path (approval.requested) follows the same rule.
	obs := &Observer{Inner: nopPub{}}
	obs.Bind(h.svc)
	ap := h.approval("ap-legal2", func(a *domain.Approval) { a.AgentID = "agent-legal" })
	_ = obs.Publish(context.Background(), domain.Event{Type: domain.EvApprovalRequest, OrgID: org, Payload: map[string]any{"approval": ap}})
	if len(h.slack.posts) != 2 {
		t.Fatal("the observer posted a private office's approval")
	}
	// Fail closed: the office cannot be told.
	h.offices(fakeOffices{broken: true})
	h.svc.Post(context.Background(), org, h.approval("ap-gen2", nil))
	if len(h.slack.posts) != 2 {
		t.Fatal("posted while the office of the approval could not be told")
	}
}

// D3 (I3): /oficina pendientes lists only the approvals whose work the person sees (an admin who is not a member of
// a private office does not see its approvals), and a button on a hidden approval answers like a missing one.
func TestSlackPendingAndDecisionsFollowTheOffices(t *testing.T) {
	h := newHarness(t)
	for id, role := range map[string]string{"admin-1": "admin", "legal-lead": "member", "legal-member": "admin", "sales-lead": "viewer"} {
		h.dir.set(id, role)
	}
	h.link("UA", "admin-1")
	h.link("UL", "legal-lead")
	h.link("UM", "legal-member")
	h.link("US", "sales-lead")
	h.offices(fakeOffices{})
	h.approval("ap-legal", func(a *domain.Approval) { a.AgentID = "agent-legal"; a.Title = "Sign the NDA with Initech" })
	h.approval("ap-gen", nil)

	if out := replyText(h.command("UA", "pendientes")); strings.Contains(out, "Initech") || !strings.Contains(out, "Send the invoice") {
		t.Fatalf("a non-member admin's pending list = %s", out)
	}
	if out := replyText(h.command("UM", "pendientes")); !strings.Contains(out, "Initech") {
		t.Fatalf("a member's pending list = %s", out)
	}
	// The responsible decides without approvals:decide: their card of the office's approval offers the buttons.
	if out := replyText(h.command("UL", "pendientes")); !strings.Contains(out, "Initech") || !strings.Contains(out, ActionApprove) {
		t.Fatalf("the responsible's pending list = %s", out)
	}

	// A non-member admin pressing a button on the private approval: not found, nothing changes.
	h.press("UA", ActionApprove, "ap-legal", "1")
	if h.get("ap-legal").Status != domain.ApprovalPending || len(h.decisions(OutcomeNotFound)) != 1 {
		t.Fatalf("a non-member decided a private office's approval: %+v", h.get("ap-legal"))
	}
	if strings.Contains(replyText(h.slack.lastResponse()), "Initech") {
		t.Fatal("the refusal shows the hidden approval")
	}
	// A member who is not its responsible (with approvals:decide): refused by the office rule.
	h.press("UM", ActionApprove, "ap-legal", "2")
	if h.get("ap-legal").Status != domain.ApprovalPending {
		t.Fatal("a member who is not the responsible decided")
	}
	// The responsible (role member, no approvals:decide) decides from Slack.
	h.press("UL", ActionApprove, "ap-legal", "3")
	if h.get("ap-legal").Status != domain.ApprovalApproved {
		t.Fatalf("the responsible could not decide: %+v / %v", h.get("ap-legal"), h.slack.lastResponse())
	}
	// Closed now: a non-member pressing again still gets "not found", never the card with the real state.
	h.press("UA", ActionReject, "ap-legal", "4")
	if len(h.decisions(OutcomeNotFound)) != 2 || strings.Contains(replyText(h.slack.lastResponse()), "Initech") {
		t.Fatalf("a closed hidden approval was shown: %v", h.slack.lastResponse())
	}
	// A viewer who is not a responsible of the approval stays refused (as before).
	h.press("US", ActionApprove, "ap-gen", "5")
	if h.get("ap-gen").Status != domain.ApprovalPending {
		t.Fatal("a viewer decided")
	}
}
