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

func TestResolveCellChecksAccessAndReadsTheCachedValue(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A2":{"v":"Margen real"},"B2":{"v":0.24},"B3":{"f":"=B2*100","v":24}}`)
	if _, ok := e.svc.ResolveCell(e.ctx, domain.DemoOrgID, "analyst", "req-1", a.ID, "Margen", "B2"); ok {
		t.Fatal("an agent without access must not resolve a cell")
	}
	e.attach(a.ID, "analyst", "read")
	if v, ok := e.svc.ResolveCell(e.ctx, domain.DemoOrgID, "analyst", "req-1", a.ID, "margen", "b3"); !ok || v != "24" {
		t.Fatalf("formula cell: %q %v", v, ok)
	}
	for _, c := range [][2]string{{"Margen", "Z99"}, {"Otra", "B2"}} {
		if _, ok := e.svc.ResolveCell(e.ctx, domain.DemoOrgID, "analyst", "req-1", a.ID, c[0], c[1]); ok {
			t.Fatalf("%v must not resolve", c)
		}
	}
	if _, ok := e.svc.ResolveCell(e.ctx, domain.DemoOrgID, "analyst", "req-1", "missing-id", "Margen", "B2"); ok {
		t.Fatal("a missing artifact must not resolve")
	}
}
