package api_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/positions"
	"aiworkforce/backend/internal/skills"
)

// Positions inside a department over HTTP (docs/architecture/positions.md, 08-api.md section 55).

type testSkillCatalog struct{ svc *skills.Service }

func (c testSkillCatalog) Skills(ctx context.Context, org string) ([]positions.SkillInfo, error) {
	l, err := c.svc.Catalog(ctx, org)
	out := []positions.SkillInfo{}
	for _, s := range l {
		out = append(out, positions.SkillInfo{ID: s.ID, Name: s.Name, OfficeID: s.OfficeID})
	}
	return out, err
}

type testPositionFolders struct{ drv *drive.Service }

func (f testPositionFolders) Bounds(ctx context.Context, org, office string) (positions.Bounds, error) {
	b := positions.Bounds{Shared: "/Shared", SharedIn: []string{}}
	if fo, err := f.drv.OfficeFolder(application.WithOrg(ctx, org), office); err == nil {
		b.Office = fo.Path
	}
	return b, nil
}

func (f testPositionFolders) FolderExists(ctx context.Context, org, p string) (bool, error) {
	fo, err := f.drv.FolderByPath(application.WithOrg(ctx, org), p)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil && fo.DeletedAt == nil, err
}

type testAgentOffices struct{ svc *offices.Service }

func (a testAgentOffices) AgentOffice(ctx context.Context, org, id string) (string, error) {
	m, err := a.svc.Model(ctx, org)
	if err != nil {
		return "", err
	}
	if o := m.Agent(id).OfficeID; o != offices.GeneralID {
		return o, nil
	}
	return "", nil
}

// withPositions wires the positions like cmd/server/positions.go does.
func withPositions(d *api.Deps) {
	rec := d.Rec
	cfg := positions.Config{Store: positions.NewMemStore(), Model: d.Offices.Model, OrgID: d.Cfg.OrgID}
	if rec != nil {
		cfg.Audit = func(ctx context.Context, org, actor, action, id string, det map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "position", EntityID: id, Details: det})
		}
	}
	if d.Skills != nil {
		cfg.Skills = testSkillCatalog{svc: d.Skills}
	}
	if d.Drive != nil {
		cfg.Folders = testPositionFolders{drv: d.Drive}
	}
	d.Positions = positions.New(cfg)
	if d.Drive != nil {
		d.Drive.SetAgentLibraries(d.Positions)
	}
	if d.Skills != nil {
		d.Skills.SetPositionSkills(d.Positions.PositionSkills)
	}
}

type posEnv struct {
	*officeApprovals
	drv *drive.Service
}

func newPosEnv(t *testing.T) *posEnv {
	t.Helper()
	pe := &posEnv{}
	pe.officeApprovals = newOfficeApprovals(t, func(o *opts) {
		o.skills = true
		o.deps = func(d *api.Deps) {
			withPositions(d)
			d.Drive.SetAgentOffices(testAgentOffices{svc: d.Offices})
			pe.drv = d.Drive
		}
	})
	return pe
}

func (p *posEnv) office(name string) string {
	p.e.t.Helper()
	for _, o := range p.e.json("GET", api1+"/offices", p.owner.AccessToken, nil, 200)["offices"].([]any) {
		if m := o.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	p.e.t.Fatalf("no office %s", name)
	return ""
}

func (p *posEnv) folder(path string) {
	p.e.t.Helper()
	ctx := application.WithOrg(context.Background(), p.org)
	if _, err := p.drv.Write(ctx, drive.Human("u"), drive.WriteInput{Path: path + "/leeme.md", Data: []byte("x")}); err != nil {
		p.e.t.Fatal(err)
	}
}

func TestPositionsOverHTTP(t *testing.T) {
	p := newPosEnv(t)
	legal, finanzas, privada := p.office("Legal"), p.office("Finanzas"), p.office("Privada")
	p.folder("/Shared/Plantillas")
	p.folder("/Shared/Precios")
	skill := ""
	for _, s := range p.e.list(api1+"/skills", p.owner.AccessToken) {
		if s["status"] == "active" && s["office_id"] == "" {
			skill = s["id"].(string)
			break
		}
	}
	base := api1 + "/offices/" + legal + "/positions"

	// the responsible of Legal (a plain member of the company) manages its positions
	v := p.e.json("GET", base, p.ana, nil, 200)
	if v["can_manage"] != true || v["bounds"].(map[string]any)["shared"] != "/Shared" {
		t.Fatalf("list: %v", v)
	}
	boss := p.e.json("POST", base, p.ana, map[string]any{"name": "Gerente de Legal", "template": "manager", "skills": []string{skill},
		"library": []map[string]any{{"path": "/Shared/Plantillas", "level": "read"}, {"path": "/Shared/Precios", "level": "read"}}}, 201)
	bossID := boss["id"].(string)
	// an assistant with more than its manager is refused, saying what exceeds
	bad := p.e.json("POST", base, p.ana, map[string]any{"name": "Asistente", "reports_to": bossID,
		"library": []map[string]any{{"path": "/Shared", "level": "read"}}}, 400)
	if bad["code"] != "exceeds_superior" || bad["details"] == nil {
		t.Fatalf("exceeds: %v", bad)
	}
	asst := p.e.json("POST", base, p.ana, map[string]any{"name": "Asistente", "reports_to": bossID, "skills": []string{skill},
		"library": []map[string]any{{"path": "/Shared/Plantillas", "level": "read"}, {"path": "/Shared/Precios", "level": "read"}}}, 201)
	if ts := p.e.json("GET", base+"/templates", p.ana, nil, 200)["templates"].([]any); len(ts) != 3 {
		t.Fatalf("templates: %v", ts)
	}

	// not the responsible of Finanzas: 403; a private department: 404 to an admin who is not a member
	if c := p.e.status("POST", api1+"/offices/"+finanzas+"/positions", p.ana, map[string]any{"name": "x"}); c != 403 {
		t.Fatalf("not its responsible = %d", c)
	}
	if c := p.e.status("GET", api1+"/offices/"+privada+"/positions", p.beto, nil); c != 404 {
		t.Fatalf("private department to a non-member admin = %d", c)
	}
	p.e.json("GET", api1+"/offices/"+privada+"/positions", p.carla, nil, 200)
	if c := p.e.status("GET", api1+"/agents/hr/position", p.beto, nil); c != 404 {
		t.Fatalf("agent of a private department = %d", c)
	}

	// assign; the agent's skills and folders say where each one comes from
	got := p.e.json("PUT", api1+"/agents/legal/position", p.ana, map[string]any{"position_id": asst["id"]}, 200)
	if got["position"].(map[string]any)["id"] != asst["id"] || got["can_manage"] != true {
		t.Fatalf("assigned: %v", got)
	}
	via := ""
	for _, s := range p.e.list(api1+"/agents/legal/skills", p.ana) {
		if s["id"] == skill {
			via, _ = s["via"].(string)
		}
	}
	if via != "position" {
		t.Fatalf("the skill comes from the position: %q", via)
	}
	fa := p.e.json("GET", api1+"/agents/legal/folder-access", p.ana, nil, 200)
	sources := map[string]string{}
	for _, e := range fa["entries"].([]any) {
		m := e.(map[string]any)
		sources[m["path"].(string)] = m["source"].(string)
	}
	if sources["/Shared/Plantillas"] != "position" || sources["/Shared"] != "" {
		t.Fatalf("folder access: %v", sources)
	}
	if c := p.e.status("PUT", api1+"/agents/accounting/position", p.ana, map[string]any{"position_id": asst["id"]}); c != 403 && c != 400 {
		t.Fatalf("an agent of another department = %d", c)
	}

	// cutting the manager: the preview says what the assistant loses, then it is applied
	cut := map[string]any{"name": "Gerente de Legal", "reports_to": "", "skills": []string{}, "library": []map[string]any{{"path": "/Shared/Plantillas", "level": "read"}}, "dry_run": true}
	pre := p.e.json("PUT", base+"/"+bossID, p.ana, cut, 200)
	ch := pre["impact"].(map[string]any)["changed"].([]any)
	if len(ch) != 1 || ch[0].(map[string]any)["position_id"] != asst["id"] {
		t.Fatalf("preview: %v", pre)
	}
	cut["dry_run"] = false
	p.e.json("PUT", base+"/"+bossID, p.ana, cut, 200)
	for _, pos := range p.e.json("GET", base, p.ana, nil, 200)["positions"].([]any) {
		m := pos.(map[string]any)
		if m["id"] == asst["id"] && (len(m["skills"].([]any)) != 0 || len(m["library"].([]any)) != 1) {
			t.Fatalf("the assistant was cut: %v", m)
		}
	}
	// an unassign and a delete
	if r := p.e.json("DELETE", api1+"/agents/legal/position", p.ana, nil, 200); r["position"] != nil {
		t.Fatalf("unassigned: %v", r)
	}
	p.e.json("DELETE", base+"/"+bossID, p.ana, nil, 200)
	if c := p.e.status("DELETE", base+"/"+bossID, p.ana, nil); c != 404 {
		t.Fatalf("deleted twice = %d", c)
	}
}
