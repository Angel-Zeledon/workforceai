package artifacts_test

import (
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func TestContextArtifactsAreBoundedAndAccessChecked(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	// no access for the agent and a different request: skipped
	if got := e.svc.ContextArtifacts(e.ctx, domain.DemoOrgID, "analyst", "req-1", []string{a.ID, "missing-id"}, 3000); len(got) != 0 {
		t.Fatalf("an agent without access must not read it: %+v", got)
	}
	e.attach(a.ID, "analyst", "read")
	got := e.svc.ContextArtifacts(e.ctx, domain.DemoOrgID, "analyst", "req-1", []string{a.ID}, 3000)
	if len(got) != 1 || got[0].ID != a.ID || !strings.Contains(got[0].Text, "A1") || got[0].Truncated {
		t.Fatalf("got %+v", got)
	}
	small := e.svc.ContextArtifacts(e.ctx, domain.DemoOrgID, "analyst", "req-1", []string{a.ID}, 5)
	if len(small) != 1 || len(small[0].Text) > 20 || !small[0].Truncated {
		t.Fatalf("budget not enforced: %+v", small)
	}
}
