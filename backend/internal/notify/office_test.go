package notify

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// D3: the approval of an office's agent is also e-mailed to its responsibles, with the links; the addresses of the
// organization still hear about it, but only the responsibles get links (the rest open the app, where the rule
// applies). An approval of General is sent as before.
func TestOfficeApprovalGoesToItsResponsibles(t *testing.T) {
	s, _, fm, _ := newSvc(t, pending("low"))
	s.Approvers = func(_ context.Context, org string, ap domain.Approval) []string {
		if org != "o1" || ap.AgentID != "legal" {
			return nil
		}
		return []string{"ana@example.com", "BOSS@example.com"}
	}
	ap := pending("low")
	ap.AgentID = "legal"
	s.Notify(context.Background(), "o1", ap)
	byTo := map[string]string{}
	for _, m := range fm.sent {
		byTo[m.To] = m.Text
	}
	if len(fm.sent) != 2 || byTo["boss@example.com"] == "" || byTo["ana@example.com"] == "" {
		t.Fatalf("sent to %v", byTo)
	}
	if a, r := linksOf(t, byTo["ana@example.com"]); a == "" || r == "" {
		t.Fatalf("the responsible got no links: %s", byTo["ana@example.com"])
	}
	if a, _ := linksOf(t, byTo["boss@example.com"]); a == "" {
		t.Fatalf("boss is a responsible too (case-insensitive) and got no links: %s", byTo["boss@example.com"])
	}

	// someone of the organization who is not a responsible: told, without links
	s2, _, fm2, _ := newSvc(t, pending("low"))
	s2.Approvers = func(context.Context, string, domain.Approval) []string { return []string{"ana@example.com"} }
	s2.Notify(context.Background(), "o1", ap)
	byTo = map[string]string{}
	for _, m := range fm2.sent {
		byTo[m.To] = m.Text
	}
	if a, _ := linksOf(t, byTo["boss@example.com"]); a != "" || byTo["boss@example.com"] == "" {
		t.Fatalf("a non-responsible got links (or nothing): %s", byTo["boss@example.com"])
	}
	if a, _ := linksOf(t, byTo["ana@example.com"]); a == "" {
		t.Fatalf("ana got no links: %s", byTo["ana@example.com"])
	}

	// General: as today
	s3, _, fm3, _ := newSvc(t, pending("low"))
	s3.Approvers = func(context.Context, string, domain.Approval) []string { return nil }
	s3.Notify(context.Background(), "o1", pending("low"))
	if len(fm3.sent) != 1 {
		t.Fatalf("General sent %d", len(fm3.sent))
	}
	if a, _ := linksOf(t, fm3.sent[0].Text); a == "" {
		t.Fatal("General lost its links")
	}
}
