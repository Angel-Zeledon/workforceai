package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/governance"
)

// D3 pending items, area A: the public API and the skill names.

// memTokenStore keeps API tokens in memory (governance.TokenStore).
type memTokenStore struct {
	mu   sync.Mutex
	rows map[string]governance.APIToken // by hash
	org  map[string]string              // hash -> org
}

func (m *memTokenStore) Insert(_ context.Context, org string, t governance.APIToken, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[h], m.org[h] = t, org
	return nil
}

func (m *memTokenStore) List(_ context.Context, org string) ([]governance.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []governance.APIToken
	for h, t := range m.rows {
		if m.org[h] == org {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memTokenStore) Revoke(context.Context, string, string, time.Time) error { return nil }

func (m *memTokenStore) Lookup(_ context.Context, h string) (string, governance.APIToken, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[h]
	if !ok {
		return "", t, "", governance.ErrNotFound
	}
	return m.org[h], t, h, nil
}

func (m *memTokenStore) Touch(context.Context, string, string, time.Time) error { return nil }

// 17. The public API (an organization's token, no person behind it) leaves out the work of private departments.
func TestPublicAPILeavesOutPrivateWork(t *testing.T) {
	tokens := &governance.TokenService{Store: &memTokenStore{rows: map[string]governance.APIToken{}, org: map[string]string{}}}
	var owner auth.Principal
	p := newPrivateWork(t, func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			withOfficesAs(d, func() auth.Principal { return owner })
			d.Governance = &api.GovernanceDeps{Tokens: tokens}
		}
	})
	owner = auth.Principal{UserID: p.owner.User.ID, OrgID: p.owner.OrgID, Role: auth.RoleOwner}
	tid := "task-legal"
	if err := p.e.store.UpdateAgentState(context.Background(), p.owner.OrgID, "legal", domain.StateWorking, "Completó: tarea legal", &tid, 60); err != nil {
		t.Fatal(err)
	}
	_, plain, err := tokens.Create(context.Background(), p.owner.OrgID, p.e.userID(p.ana), "integración",
		[]string{governance.ScopeAgentsRead, governance.ScopeRequestsRead, governance.ScopeReportsRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, want int) string {
		t.Helper()
		resp, raw := p.e.do("GET", "/api/public/v1"+path, plain, nil, nil)
		if resp.StatusCode != want {
			t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, want, raw)
		}
		return string(raw)
	}
	if got := get("/agents", 200); strings.Contains(got, "tarea legal") || !strings.Contains(got, `"legal"`) {
		t.Fatalf("public agents = %s", got)
	}
	for path, shown := range map[string]string{"/requests": "req-sales", "/reports": "rep-sales"} {
		if got := get(path, 200); strings.Contains(got, "legal") || !strings.Contains(got, shown) {
			t.Fatalf("public %s = %s", path, got)
		}
	}
	get("/requests/req-legal", 404)
	get("/reports/rep-legal", 404)
	get("/requests/req-sales", 200)
	get("/reports/rep-sales", 200)
}

// 18. A taken skill name answers the same whatever skill holds it: one of a private department is not revealed.
func TestSkillNameTakenRevealsNothing(t *testing.T) {
	p := newPrivateWork(t, withSkills)
	skill := func(name, office string) map[string]any {
		b := map[string]any{"name": name, "description": "Use for " + name, "instructions": "# " + name + "\n\nSteps.\n"}
		if office != "" {
			b["office_id"] = office
		}
		return b
	}
	p.e.json("POST", api1+"/skills", p.ana, skill("contrato-secreto", p.dep), 201)
	gone := p.e.json("POST", api1+"/skills", p.ana, skill("borrado-secreto", p.dep), 201)
	p.e.json("POST", api1+"/skills", p.beto, skill("comun", ""), 201)
	if c := p.e.status("DELETE", api1+"/skills/"+str(gone, "id"), p.ana, nil); c != 204 && c != 200 {
		t.Fatalf("delete = %d", c)
	}
	conflict := func(name string) string {
		t.Helper()
		resp, raw := p.e.do("POST", api1+"/skills", p.beto, skill(name, ""), nil)
		if resp.StatusCode != 409 {
			t.Fatalf("beto creates %q = %d %s", name, resp.StatusCode, raw)
		}
		return string(raw)
	}
	visible, hidden, deleted := conflict("comun"), conflict("contrato-secreto"), conflict("borrado-secreto")
	if hidden != visible || deleted != visible || strings.Contains(hidden, "secreto") || !strings.Contains(hidden, "name_taken") {
		t.Fatalf("409 bodies: visible %s, hidden %s, deleted %s", visible, hidden, deleted)
	}
}
