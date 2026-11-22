package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/agenthistory"
	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
)

// F4: the configuration history of each agent over HTTP (docs/architecture/agent-history.md).

type histEnv struct {
	*officeApprovals
	hist   *agenthistory.Service
	cfg    application.ConfigStore
	rec    *application.Recorder
	router http.Handler
	orch   *application.Orchestrator
}

func newHistEnv(t *testing.T) *histEnv {
	t.Helper()
	h := &histEnv{}
	p := newOfficeApprovals(t, func(o *opts) {
		o.v2, o.skills, o.trust, o.rehearsal = true, true, true, true
		o.deps = func(d *api.Deps) {
			cs := d.Profiles.Tones
			d.OrgConfig = &application.OrgConfig{Cfg: d.Cfg, Store: cs, Core: d.Store, Orch: d.Orch, Rec: d.Rec, Log: d.Log}
			rec := d.Rec
			d.Controls = controls.New(controls.NewMemStore(),
				func(c context.Context, org, actor, action, entity, id string, det map[string]any) {
					rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: det})
				},
				func(c context.Context, org, typ string, p map[string]any) {
					id, _ := p["agent_id"].(string)
					rec.Emit(application.WithOrg(c, org), application.Action{Type: typ, AgentID: id, Payload: p, SkipAudit: true})
				})
			d.Permissions = &application.PermissionsService{Store: cs, Agents: d.Store, Rec: d.Rec, OrgID: d.Cfg.OrgID, Log: d.Log,
				SetLevel: d.Trust.ChooseLevel}
			withPositions(d) // positions inside a department: the position is part of the snapshot
			h.hist = api.NewAgentHistory(*d, agenthistory.NewMemStore())
			obs := &agenthistory.Observer{Inner: rec.Pub}
			obs.Bind(h.hist)
			rec.Pub = obs
			d.History = h.hist
			h.cfg, h.rec, h.orch = cs, rec, d.Orch
		}
	})
	h.officeApprovals = p
	h.router = p.e.srv.Config.Handler
	return h
}

func (h *histEnv) last(agent string) agenthistory.Version {
	h.e.t.Helper()
	h.hist.Flush()
	v, _, err := h.hist.Store.Last(context.Background(), h.org, agent)
	if err != nil {
		h.e.t.Fatal(err)
	}
	return v
}

func (h *histEnv) versions(agent, token string) []map[string]any {
	h.e.t.Helper()
	h.hist.Flush()
	got := h.e.json("GET", api1+"/agents/"+agent+"/versions", token, nil, 200)
	out := []map[string]any{}
	for _, v := range got["versions"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func fieldsOf(v map[string]any) []string {
	out := []string{}
	for _, f := range v["fields"].([]any) {
		out = append(out, f.(string))
	}
	return out
}

type change struct {
	method, path string
	body         any
	field        string // a field the version must list
}

// Every route that changes an agent's configuration leaves a version naming what changed, and a router walk fails
// on any new route that changes an agent without being classified here.
func TestEveryAgentChangeRouteLeavesAVersion(t *testing.T) {
	h := newHistEnv(t)
	tok := h.owner.AccessToken
	ana := h.e.userID(h.ana)
	starters := h.e.list(api1+"/skills", tok)
	skill := ""
	for _, s := range starters {
		if s["status"] == "active" {
			skill = s["id"].(string)
			break
		}
	}
	if skill == "" {
		t.Fatal("no active starter skill")
	}
	base := api1 + "/agents/sales"
	steps := []change{
		{"PUT", base + "/profile", map[string]any{"instructions": "Saluda siempre.", "creativity": "medium", "skills": []string{}}, "instructions"},
		{"PUT", base + "/profile", map[string]any{"instructions": "Despídete siempre.", "creativity": "high", "skills": []string{"cotizar"}}, "creativity"},
		{"POST", base + "/profile/restore", map[string]any{"version": 1}, "instructions"},
		{"PUT", base + "/tone", map[string]any{"tone": "mx"}, "tone"},
		{"PUT", base + "/budget", map[string]any{"monthly_budget_usd": 40}, "budget_usd"},
		{"PUT", base + "/job", map[string]any{"responsibilities": []string{"Cotizar a clientes"}}, "job"},
		{"PUT", base + "/skills/" + skill, nil, "skills"},
		{"DELETE", base + "/skills/" + skill, nil, "skills"},
		{"PUT", base + "/permissions/level", map[string]any{"level": "important"}, "permissions"},
		{"PUT", base + "/permissions/exceptions/messages", map[string]any{"mode": "never"}, "permissions"},
		{"DELETE", base + "/permissions/exceptions/messages", nil, "permissions"},
		{"POST", base + "/permissions/floor/payments", map[string]any{"understood": true}, "permissions"},
		{"DELETE", base + "/permissions/floor/payments", nil, "permissions"},
		{"POST", base + "/control", map[string]any{"action": "pause"}, "paused"},
		{"POST", base + "/control", map[string]any{"action": "resume"}, "paused"},
		{"POST", base + "/control", map[string]any{"action": "read_only"}, "read_only"},
		{"PUT", base + "/rehearsal", map[string]any{"enabled": true}, "rehearsal"},
		{"PUT", base + "/access", map[string]any{"visibility": "people", "shares": []map[string]string{{"user_id": ana, "level": "chat"}}}, "access"},
		{"PUT", base + "/folder-access", map[string]any{"path": "/Clients/Acme", "level": "edit"}, "folders"},
	}
	covered := map[string]bool{}
	h.versions("sales", tok) // the baseline: how it was when the history started
	prev := h.last("sales").Version
	if prev != 1 {
		t.Fatalf("baseline version = %d", prev)
	}
	for _, st := range steps {
		resp, raw := h.e.do(st.method, st.path, tok, st.body, nil)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			t.Fatalf("%s %s = %d: %s", st.method, st.path, resp.StatusCode, raw)
		}
		v := h.last("sales")
		if v.Version != prev+1 || !slices.Contains(v.Fields, st.field) {
			t.Fatalf("%s %s: version %d (was %d) fields %v, want a new version with %q", st.method, st.path, v.Version, prev, v.Fields, st.field)
		}
		if v.ActorKind != agenthistory.ActorPerson || v.Actor == "" || !strings.HasPrefix(v.Source, st.method+" /agents/{id}") {
			t.Fatalf("%s %s: who/how = %+v", st.method, st.path, v)
		}
		prev = v.Version
		covered[st.method+" "+strings.Replace(strings.TrimPrefix(st.path, api1), "/agents/sales", "/agents/{id}", 1)] = true
	}
	covered["PUT /agents/{id}/skills/{skillId}"], covered["DELETE /agents/{id}/skills/{skillId}"] = true, true
	covered["PUT /agents/{id}/permissions/exceptions/{category}"], covered["DELETE /agents/{id}/permissions/exceptions/{category}"] = true, true
	covered["POST /agents/{id}/permissions/floor/{category}"], covered["DELETE /agents/{id}/permissions/floor/{category}"] = true, true

	// A failed change leaves nothing.
	if c := h.e.status("PUT", base+"/tone", tok, map[string]any{"tone": "klingon"}); c != 400 {
		t.Fatalf("bad tone = %d", c)
	}
	if v := h.last("sales"); v.Version != prev {
		t.Fatalf("a refused change left version %d", v.Version)
	}

	// Routes that change an agent but are not its configuration (its work, its memory, its look).
	notConfig := map[string]string{
		"DELETE /agents/{id}/memory/{docId}":         "forgetting a memory is the agent's knowledge, not its configuration",
		"POST /agents/{id}/golden-cases":             "quality test cases",
		"POST /agents/{id}/trust/evaluate":           "an evaluation; a demotion it applies is recorded as the system's",
		"PUT /agents/{id}/equipment/{slot}":          "cosmetic items bought with coins",
		"DELETE /agents/{id}/equipment/{slot}":       "cosmetic items bought with coins",
		"POST /agents/{id}/versions/{v}/restore":     "the restore itself (tested below)",
		"PUT /governance/agents/{id}/department":     "governance needs Postgres; recorded by the same hook",
		"POST /agents/{id}/profile/restore":          "covered above",
		"PUT /agents/{id}/permissions/level":         "covered above",
		"POST /agents/{id}/control":                  "covered above",
		"PUT /agents/{id}/folder-access":             "covered above",
		"PUT /agents/{id}/access":                    "covered above",
		"PUT /agents/{id}/rehearsal":                 "covered above",
		"PUT /agents/{id}/profile":                   "covered above",
		"PUT /agents/{id}/tone":                      "covered above",
		"PUT /agents/{id}/budget":                    "covered above",
		"PUT /agents/{id}/job":                       "covered above",
		"PUT /agents/{id}/position":                  "covered by TestAgentPositionLeavesAVersion",
		"DELETE /agents/{id}/position":               "covered by TestAgentPositionLeavesAVersion",
	}
	routes, ok := h.router.(chi.Routes)
	if !ok {
		t.Fatal("the router is not walkable")
	}
	seen := 0
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet || method == http.MethodHead || !strings.Contains(route, "/agents/{id}") {
			return nil
		}
		k := method + " " + strings.TrimPrefix(route, "/api/v1")
		seen++
		if !covered[k] && notConfig[k] == "" {
			return fmt.Errorf("route %s changes an agent and is not classified: add it to the history test (does it leave a version?)", k)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen < 15 {
		t.Fatalf("walked only %d agent routes", seen)
	}
}

// The timeline: a baseline, who changed what, an automatic demotion as the system's, and the detail field by field.
func TestTimelineAndDetail(t *testing.T) {
	h := newHistEnv(t)
	tok := h.owner.AccessToken
	h.e.json("PUT", api1+"/agents/sales/profile", tok, map[string]any{"instructions": "Responde en menos de 5 líneas.", "creativity": "medium", "skills": []string{}}, 200)
	// the trust ladder demotes the agent on its own, in the background of the request of the person who rejected
	// (its context still carries them)
	beto := h.e.userID(h.beto)
	ctx := auth.WithPrincipal(application.WithActor(application.WithOrg(context.Background(), h.org), beto), auth.Principal{UserID: beto, OrgID: h.org, Role: auth.RoleAdmin})
	a, _ := h.e.store.GetAgent(ctx, h.org, "sales")
	if err := h.cfg.SetAgentAutonomy(ctx, h.org, "sales", "suggest"); err != nil {
		t.Fatal(err)
	}
	h.rec.Emit(ctx, application.Action{Type: domain.EvAutonomyChanged, AgentID: "sales", Entity: "agent", EntityID: "sales", SkipAudit: true,
		Payload: map[string]any{"agent_id": "sales", "from": a.Autonomy, "to": "suggest", "source": "auto_demotion"}})

	vs := h.versions("sales", h.ana)
	if len(vs) != 3 {
		t.Fatalf("timeline: %v", vs)
	}
	if vs[2]["source"] != "baseline" || vs[2]["actor_kind"] != "system" {
		t.Fatalf("first line is the baseline: %v", vs[2])
	}
	if vs[1]["actor_kind"] != "person" || !slices.Contains(fieldsOf(vs[1]), "instructions") || vs[1]["actor_name"] == "" {
		t.Fatalf("person's change: %v", vs[1])
	}
	if vs[0]["actor_kind"] != "system" || vs[0]["source"] != "trust.auto_demotion" || !slices.Equal(fieldsOf(vs[0]), []string{"autonomy"}) {
		t.Fatalf("automatic demotion: %v", vs[0])
	}
	if _, ok := vs[0]["config"]; ok {
		t.Fatal("the timeline carries the snapshots")
	}
	d := h.e.json("GET", api1+"/agents/sales/versions/2", h.ana, nil, 200)
	ch := d["changes"].([]any)
	if len(ch) != 1 || ch[0].(map[string]any)["field"] != "instructions" || ch[0].(map[string]any)["to"] != "Responde en menos de 5 líneas." {
		t.Fatalf("detail: %v", d)
	}
	if c := h.e.status("GET", api1+"/agents/sales/versions/99", h.ana, nil); c != 404 {
		t.Fatalf("missing version = %d", c)
	}
	if c := h.e.status("GET", api1+"/agents/sales/versions/abc", h.ana, nil); c != 400 {
		t.Fatalf("bad version = %d", c)
	}
}

// Hiring (a route that names no agent) starts the new agent's history through its event, as the person's.
func TestHireStartsTheHistory(t *testing.T) {
	h := newHistEnv(t)
	res := h.e.json("POST", api1+"/agents/from-template", h.owner.AccessToken, map[string]any{"template_id": "project_manager", "locale": "es"}, 201)
	id := res["agent"].(map[string]any)["id"].(string)
	v := h.last(id)
	if v.Version != 1 || v.Source != agenthistory.SourceCreated || v.ActorKind != agenthistory.ActorPerson || v.ActorName == "" {
		t.Fatalf("hire version: %+v", v)
	}
	vs := h.versions(id, h.owner.AccessToken)
	if len(vs) != 1 || vs[0]["source"] != "agent.created" {
		t.Fatalf("timeline of a new agent: %v", vs)
	}
}

func (h *histEnv) restore(agent string, v int, token string, want int) map[string]any {
	h.e.t.Helper()
	return h.e.json("POST", fmt.Sprintf("%s/agents/%s/versions/%d/restore", api1, agent, v), token, nil, want)
}

func skippedReasons(res map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range res["skipped"].([]any) {
		m := s.(map[string]any)
		k := m["field"].(string)
		if c, ok := m["category"].(string); ok {
			k += ":" + c
		}
		out[k] = m["reason"].(string)
	}
	return out
}

// Restoring goes through the services of each field: a new version, no more autonomy, no fewer approvals, no
// skill that is gone; and the preview says beforehand what will not come back.
func TestRestoreOverHTTPIsSafe(t *testing.T) {
	h := newHistEnv(t)
	tok := h.owner.AccessToken
	base := api1 + "/agents/analyst"
	starters := h.e.list(api1+"/skills", tok)
	skill := ""
	for _, s := range starters {
		if s["status"] == "active" {
			skill = s["id"].(string)
			break
		}
	}
	// v1 baseline; then the agent is set up: level "important", a skill, instructions A, cap 30
	h.e.json("PUT", base+"/permissions/level", tok, map[string]any{"level": "alone"}, 200)
	h.e.json("PUT", base+"/permissions/exceptions/publish", tok, map[string]any{"mode": "never"}, 200)
	h.e.json("PUT", base+"/skills/"+skill, tok, nil, 200)
	h.e.json("PUT", base+"/profile", tok, map[string]any{"instructions": "Versión A", "creativity": "medium", "skills": []string{}}, 200)
	h.e.json("PUT", base+"/budget", tok, map[string]any{"monthly_budget_usd": 30}, 200)
	good := h.last("analyst").Version
	if a := h.last("analyst").Config.Autonomy; a != "autonomous" {
		t.Fatalf("set up: autonomy %s", a)
	}
	// since then: the system lowered the autonomy, a person asked for approvals of what it publishes, the skill was
	// disabled and unassigned, the instructions changed
	ctx := application.WithOrg(context.Background(), h.org)
	h.e.json("PUT", base+"/permissions/level", tok, map[string]any{"level": "important"}, 200)
	h.e.json("PUT", base+"/permissions/exceptions/publish", tok, map[string]any{"mode": "ask"}, 200)
	h.e.json("DELETE", base+"/skills/"+skill, tok, nil, 200)
	h.e.json("POST", api1+"/skills/"+skill+"/disable", tok, nil, 200)
	h.e.json("PUT", base+"/profile", tok, map[string]any{"instructions": "Versión B", "creativity": "medium", "skills": []string{}}, 200)
	before := h.last("analyst").Version

	// the preview
	p := h.e.json("GET", fmt.Sprintf("%s/versions/%d/restore", base, good), h.ana, nil, 200)
	pr := skippedReasons(map[string]any{"skipped": p["skip"]})
	if pr["autonomy"] != agenthistory.ReasonAutonomyEarned || pr["permissions:publish"] != agenthistory.ReasonPermissionsFreer || pr["skills"] != agenthistory.ReasonSkillUnavailable {
		t.Fatalf("preview: %v", p)
	}
	if n := h.last("analyst").Version; n != before {
		t.Fatal("the preview changed something")
	}

	// a viewer, a non-member of nothing special: viewers may not restore
	viewer := h.e.member(h.owner, "vera-hist@example.com", auth.RoleViewer)
	h.restore("analyst", good, viewer, 403)

	res := h.restore("analyst", good, tok, 200)
	sk := skippedReasons(res)
	if sk["autonomy"] != agenthistory.ReasonAutonomyEarned || sk["permissions:publish"] != agenthistory.ReasonPermissionsFreer || sk["skills"] != agenthistory.ReasonSkillUnavailable {
		t.Fatalf("skipped: %v", res)
	}
	nv := h.last("analyst")
	if nv.Version != before+1 || nv.RestoredFrom == nil || *nv.RestoredFrom != good || nv.Source != "restore" {
		t.Fatalf("new version: %+v", nv)
	}
	if nv.Config.Instructions != "Versión A" || nv.Config.Autonomy != "rules" || len(nv.Config.Skills) != 0 {
		t.Fatalf("restored config: %+v", nv.Config)
	}
	if e := exceptionMode(nv.Config, "publish"); e != "ask" {
		t.Fatalf("publish exception relaxed to %q", e)
	}
	ag, _ := h.e.store.GetAgent(ctx, h.org, "analyst")
	if ag.Autonomy != "rules" {
		t.Fatalf("autonomy = %s", ag.Autonomy)
	}
	if old, _ := h.hist.Store.Get(ctx, h.org, "analyst", before); old.Config.Instructions != "Versión B" {
		t.Fatal("history rewritten")
	}
	// audited
	logs := h.e.store.Audit()
	found := false
	for _, l := range logs {
		if l.Action == "agent.config_restored" && l.EntityID == "analyst" {
			found = true
		}
	}
	if !found {
		t.Fatal("restore not audited")
	}

	// lowering is allowed, through the ladder
	low := h.last("analyst").Version
	h.e.json("PUT", base+"/permissions/level", tok, map[string]any{"level": "alone"}, 200)
	if h.restore("analyst", low, tok, 200)["version"] == nil {
		t.Fatal("lowering back did not restore")
	}
	if ag, _ := h.e.store.GetAgent(ctx, h.org, "analyst"); ag.Autonomy != "rules" {
		t.Fatalf("autonomy after lowering = %s", ag.Autonomy)
	}
}

func exceptionMode(c agenthistory.Config, cat string) string {
	if c.Permissions == nil {
		return ""
	}
	for _, e := range c.Permissions.Exceptions {
		if e.Category == cat {
			return e.Mode
		}
	}
	return ""
}

// A spend cap never comes back above the department's limit.
func TestRestoreKeepsTheCapUnderTheDepartment(t *testing.T) {
	h := newHistEnv(t)
	tok := h.owner.AccessToken
	h.e.json("PUT", api1+"/agents/sales/budget", tok, map[string]any{"monthly_budget_usd": 500}, 200)
	high := h.last("sales").Version
	h.e.json("PUT", api1+"/agents/sales/budget", tok, map[string]any{"monthly_budget_usd": 20}, 200)
	dept := "Ventas"
	ctx := application.WithOrg(context.Background(), h.org)
	if err := h.e.store.SetAgentDepartment(ctx, h.org, "sales", &dept); err != nil {
		t.Fatal(err)
	}
	h.orch.Budget().DepartmentLimitsFor = func(context.Context, string) map[string]float64 { return map[string]float64{"Ventas": 100} }
	res := h.restore("sales", high, tok, 200)
	if skippedReasons(res)["budget_usd"] != agenthistory.ReasonBudgetAboveCeiling {
		t.Fatalf("cap above the department: %v", res)
	}
	if v := h.last("sales"); v.Config.BudgetUSD != 20 {
		t.Fatalf("cap = %v", v.Config.BudgetUSD)
	}
}

// Sealed departments and other organizations: what is hidden answers like what does not exist.
func TestHistoryIsSealedAndIsolated(t *testing.T) {
	h := newHistEnv(t)
	carla := h.carla // member and lead of Privada (hr)
	h.e.json("PUT", api1+"/agents/hr/profile", carla, map[string]any{"instructions": "Confidencial", "creativity": "medium", "skills": []string{}}, 200)
	for _, path := range []string{"/agents/hr/versions", "/agents/hr/versions/1", "/agents/hr/versions/1/restore"} {
		if c := h.e.status("GET", api1+path, h.beto, nil); c != 404 {
			t.Fatalf("non-member admin GET %s = %d", path, c)
		}
	}
	h.restore("hr", 1, h.beto, 404)
	if vs := h.versions("hr", carla); len(vs) < 2 {
		t.Fatalf("member timeline: %v", vs)
	}
	h.restore("hr", 1, carla, 200)

	// another organization: its own history of its own agents, never this one's
	rival := h.e.register("rival-hist@example.com", "RivalHist")
	for _, agent := range []string{"hr", "sales"} {
		resp, raw := h.e.do("GET", api1+"/agents/"+agent+"/versions", rival.AccessToken, nil, nil)
		if resp.StatusCode == 404 { // the rival has no such agent: nothing to read
			continue
		}
		var l struct {
			Versions []struct {
				Version int `json:"version"`
			} `json:"versions"`
		}
		if resp.StatusCode != 200 || json.Unmarshal(raw, &l) != nil {
			t.Fatalf("rival GET %s versions = %d %s", agent, resp.StatusCode, raw)
		}
		for _, v := range l.Versions {
			_, d := h.e.do("GET", fmt.Sprintf("%s/agents/%s/versions/%d", api1, agent, v.Version), rival.AccessToken, nil, nil)
			if strings.Contains(string(d), "Confidencial") {
				t.Fatal("another organization reads this history")
			}
		}
		if len(l.Versions) > 1 {
			t.Fatalf("the rival's %s has a history it never made: %+v", agent, l.Versions)
		}
	}
	// and the rival cannot restore this organization's versions onto anything
	if c := h.e.status("POST", api1+"/agents/hr/versions/3/restore", rival.AccessToken, nil); c != 404 && c != 403 {
		t.Fatalf("rival restores = %d", c)
	}
}

// Holding a position (positions.md) is part of the agent's configuration: giving and taking it leave a version, which
// is never restored by "volver a como estaba" (positions are granted by hand).
func TestAgentPositionLeavesAVersion(t *testing.T) {
	h := newHistEnv(t)
	tok := h.owner.AccessToken
	legal := ""
	for _, o := range h.e.json("GET", api1+"/offices", tok, nil, 200)["offices"].([]any) {
		if m := o.(map[string]any); m["name"] == "Legal" {
			legal = m["id"].(string)
		}
	}
	pos := h.e.json("POST", api1+"/offices/"+legal+"/positions", tok, map[string]any{"name": "Abogada"}, 201)
	h.versions("legal", tok) // baseline
	prev := h.last("legal").Version
	h.e.json("PUT", api1+"/agents/legal/position", tok, map[string]any{"position_id": pos["id"]}, 200)
	v := h.last("legal")
	if v.Version != prev+1 || !slices.Contains(v.Fields, "position") || v.Config.Position != "Abogada" {
		t.Fatalf("assign: %+v", v)
	}
	h.e.json("DELETE", api1+"/agents/legal/position", tok, nil, 200)
	if v := h.last("legal"); v.Version != prev+2 || v.Config.Position != "" {
		t.Fatalf("unassign: %+v", v)
	}
	pre := h.e.json("GET", api1+"/agents/legal/versions/"+fmt.Sprint(prev+1)+"/restore", tok, nil, 200)
	for _, st := range pre["apply"].([]any) {
		if st.(map[string]any)["field"] == "position" {
			t.Fatalf("a position is never restored: %v", pre)
		}
	}
}
