package away_test

import (
	"testing"

	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/domain"
)

// D3: with the office rule (Viewer.Decides) "decide now" follows it, not only the role.
func TestCanDecideFollowsTheOfficeRule(t *testing.T) {
	ap := domain.Approval{ID: "a", AgentID: "legal", Status: domain.ApprovalPending}
	admin := away.Viewer{UserID: "beto", Role: "admin", CanDecide: true, Decides: func(domain.Approval) bool { return false }}
	if away.CanDecide(ap, admin) {
		t.Fatal("an admin outside the office must not be asked to decide its approval")
	}
	lead := away.Viewer{UserID: "ana", Role: "member", Decides: func(a domain.Approval) bool { return a.ID == "a" }}
	if !away.CanDecide(ap, lead) {
		t.Fatal("the office's responsible decides it without approvals:decide")
	}
}
