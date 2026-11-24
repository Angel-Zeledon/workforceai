package skills

import (
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/offices"
)

// An agent that works for several offices has, in each request, the skills of the office the request is asked from and
// never the ones of its other offices: the skills of Legal are not its own in a request from Ventas (its base), the
// skills of Ventas are not in a request from Legal, and with no office known only the base's count.
func TestAgentSkillsFollowTheOfficeOfTheRequest(t *testing.T) {
	e := newOfficeEnvMulti(t, true)
	legal, err := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	if err != nil {
		t.Fatal(err)
	}
	sales, err := e.svc.Create(e.ctx, boss, officeIn("sales-kit", "d-sales"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Detail{legal, sales} {
		if _, err := e.svc.Review(e.ctx, Actor{ID: "u-ana", Admin: true}, d.ID, true, 1, ""); err != nil {
			t.Fatalf("review %q (%s): %v", d.Name, d.ID, err)
		}
	}
	e.svc.cfg.RoleDefaults = func(string) []string { return []string{"contract-review", "sales-kit"} } // both are defaults of the role
	admin := offices.Viewer{UserID: "u-owner", Admin: true}
	// Valeria's base is Ventas; Legal also accepts her
	if _, err := e.off.GrantOffice(e.ctx, org, admin, "a-valeria", "d-legal"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.off.AcceptOffice(e.ctx, org, admin, "a-valeria", "d-legal"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // the offices model is cached for TTL (a coarse clock on Windows)
	has := func(office string) map[string]bool {
		t.Helper()
		l, err := e.svc.AgentSkills(application.WithRequestOffice(e.ctx, office), "a-valeria")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, s := range l {
			out[s.Name] = true
		}
		return out
	}
	for _, c := range []struct {
		asked      string
		legal, kit bool
	}{{"d-legal", true, false}, {"d-sales", false, true}, {"", false, true}, {"d-unknown", false, true}} {
		if got := has(c.asked); got["contract-review"] != c.legal || got["sales-kit"] != c.kit {
			t.Errorf("request asked from %q: skills %v, want contract-review=%v sales-kit=%v", c.asked, got, c.legal, c.kit)
		}
	}
	// without the grant the agent never has Legal's skills, whatever office the request names
	if _, err := e.off.RemoveOffice(e.ctx, org, admin, "a-valeria", "d-legal"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := has("d-legal"); got["contract-review"] || !got["sales-kit"] {
		t.Errorf("an agent that no longer works for Legal, asked from Legal: %v", got)
	}
}
