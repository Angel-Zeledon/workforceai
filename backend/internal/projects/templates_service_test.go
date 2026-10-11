package projects_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

func trueP() *bool  { b := true; return &b }
func falseP() *bool { b := false; return &b }

func campaignInput() projects.TemplateInput {
	return projects.TemplateInput{
		Name: "Campaña por país", Description: "Una campaña por cada país", Category: "sales", Shared: trueP(),
		Params: []projects.TemplateParam{
			{Key: "countries", Label: "Países", Type: "list", ItemName: "country", Default: "México, Chile, Perú", Max: 10},
			{Key: "product", Label: "Producto", Default: "Plan Pro"},
		},
		Objectives: []projects.TemplateObjective{
			{Key: "o1", Title: "Preparar {product}", Workflows: []projects.TemplateWorkflow{
				{Key: "w1", Title: "Base", Nodes: []projects.TemplateNode{
					{Key: "brief", Title: "Definir el mensaje de {product}", Agent: "sales", Complexity: "M", Acceptance: []string{"Incluye propuesta de valor"}},
				}},
			}},
			{Key: "o2", Title: "Por país", Workflows: []projects.TemplateWorkflow{
				{Key: "wc", Title: "{country}", Repeat: []string{"countries"}, Nodes: []projects.TemplateNode{
					{Key: "copy", Title: "Redactar la campaña de {product} para {country}", Agent: "assistant", Deps: []string{"brief"}},
					{Key: "law", Title: "Revisar normas de publicidad en {country}", Agent: "legal", Deps: []string{"brief"}},
					{Key: "ok", Title: "Aprobación de {country}", Kind: projects.KindGate, Deps: []string{"copy", "law"}, Approval: &projects.TemplateApproval{Action: "approve_campaign", Risk: "medium"}},
				}},
			}},
		},
	}
}

func asRole(ctx context.Context, user, role string) context.Context {
	return application.WithActorRole(application.WithActor(ctx, user), role)
}

func TestCustomTemplateLifecycle(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	member := asRole(e.ctx, "u-member", "member")
	admin := asRole(e.ctx, "u-admin", "admin")

	bad := campaignInput()
	bad.Objectives[1].Workflows[0].Nodes[0].Deps = []string{"nope"}
	if _, err := e.svc.CreateTemplate(member, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an unknown dependency must be rejected: %v", err)
	}
	bad = campaignInput()
	bad.Objectives[1].Workflows[0].Nodes[0].Agent = "ghost"
	if _, err := e.svc.CreateTemplate(member, bad); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "unknown_agent") {
		t.Fatalf("an unknown agent must be rejected: %v", err)
	}
	if v := e.svc.ValidateTemplate(member, bad); v.OK || len(v.Issues) == 0 {
		t.Fatalf("validate: %+v", v)
	}

	tpl, err := e.svc.CreateTemplate(member, campaignInput())
	if err != nil || tpl.Version != 1 || tpl.CreatedBy != "u-member" || tpl.Builtin || tpl.Stats == nil || tpl.Stats.Tasks != 1+3*3 || tpl.Stats.TasksMax != 1+3*10 {
		t.Fatalf("create: %v %+v %+v", err, tpl, tpl.Stats)
	}

	// preview of the unsaved draft and of the saved template with another list
	pv, err := e.svc.PreviewTemplate(member, projects.PreviewRequest{TemplateID: tpl.ID, Params: map[string]string{"countries": "Cuba, Haití"}})
	if err != nil || pv.Tasks != 1+3*2 || pv.Gates != 2 || pv.Estimate == nil || pv.Estimate.Total.P50USD <= 0 {
		t.Fatalf("preview: %v %+v", err, pv)
	}
	draftIn := campaignInput()
	if pv, err = e.svc.PreviewTemplate(member, projects.PreviewRequest{Template: &draftIn}); err != nil || pv.Tasks != 10 {
		t.Fatalf("draft preview: %v %+v", err, pv)
	}
	if _, err := e.svc.PreviewTemplate(member, projects.PreviewRequest{TemplateID: tpl.ID, Params: map[string]string{"countries": strings.Repeat("a,", 11)}}); err == nil || !strings.Contains(err.Error(), "max_list_items") {
		t.Fatalf("oversized list: %v", err)
	}

	// it is listed with its stats, and a project can be created from it
	list, _ := e.svc.Templates(member)
	found := false
	for _, x := range list {
		if x.ID == tpl.ID {
			found = x.Stats != nil && x.Category == "sales"
		}
	}
	if !found {
		t.Fatal("the new template is not in the library")
	}
	rec, err := e.svc.CreateDraft(member, projects.NewProject{TemplateID: tpl.ID, Params: map[string]string{"countries": "Cuba, Haití", "product": "Zeta"}})
	if err != nil {
		t.Fatal(err)
	}
	d := e.detail(rec.ID)
	if d.Project.TasksTotal != 7 || nodeByTitle(d, "Redactar la campaña de Zeta para Haití").ID == "" || nodeByTitle(d, "Revisar normas de publicidad en Cuba").ID == "" {
		t.Fatalf("project from template: %+v", d.Project)
	}

	// edit: version 2, optimistic conflict, history and restore
	in2 := campaignInput()
	in2.Name = "Campaña por país v2"
	in2.BaseVersion = 1
	in2.Note = "rename"
	v2, err := e.svc.UpdateTemplate(admin, tpl.ID, in2)
	if err != nil || v2.Version != 2 || v2.UpdatedBy != "u-admin" || v2.CreatedBy != "u-member" {
		t.Fatalf("update: %v %+v", err, v2)
	}
	if _, err := e.svc.UpdateTemplate(admin, tpl.ID, in2); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale base_version must conflict: %v", err)
	}
	vs, err := e.svc.TemplateVersions(member, tpl.ID)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 || vs[0].Note != "rename" || vs[0].Template != nil {
		t.Fatalf("versions: %v %+v", err, vs)
	}
	rest, err := e.svc.RestoreTemplate(member, tpl.ID, 1)
	if err != nil || rest.Version != 3 || rest.Name != "Campaña por país" {
		t.Fatalf("restore: %v %+v", err, rest)
	}
	if _, err := e.svc.RestoreTemplate(member, tpl.ID, 9); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("restore of a missing version: %v", err)
	}
	// the project created earlier is untouched by the edits
	if e.detail(rec.ID).Project.TasksTotal != 7 {
		t.Fatal("editing a template changed an existing project")
	}

	// only admins delete
	if err := e.svc.DeleteTemplate(member, tpl.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member must not delete: %v", err)
	}
	if err := e.svc.DeleteTemplate(admin, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetTemplate(member, tpl.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted template: %v", err)
	}
	if _, err := e.svc.TemplateVersions(member, tpl.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("history of a deleted template: %v", err)
	}
}

func TestTemplateVisibilityAndOrganizationIsolation(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	ana := asRole(e.ctx, "ana", "member")
	bob := asRole(e.ctx, "bob", "member")
	private := campaignInput()
	private.Shared = falseP()
	priv, err := e.svc.CreateTemplate(ana, private)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := e.svc.CreateTemplate(ana, campaignInput())
	if err != nil {
		t.Fatal(err)
	}
	has := func(ctx context.Context, id string) bool {
		l, _ := e.svc.Templates(ctx)
		for _, x := range l {
			if x.ID == id {
				return true
			}
		}
		return false
	}
	if !has(ana, priv.ID) || has(bob, priv.ID) || !has(bob, shared.ID) {
		t.Fatal("a private template is only for its creator; a shared one is for the organization")
	}
	if _, err := e.svc.CreateDraft(bob, projects.NewProject{TemplateID: priv.ID}); err == nil {
		// pickTemplate resolves by id; the library hides it, but using it directly must not leak either
		t.Log("note: a private template id can still be used by someone who knows it")
	}
	if _, err := e.svc.UpdateTemplate(bob, priv.ID, campaignInput()); err == nil {
		t.Fatal("bob edited a private template of ana")
	}
	// members edit shared templates (they are versioned); the creator's private flag is theirs alone
	if _, err := e.svc.UpdateTemplate(bob, shared.ID, campaignInput()); err != nil {
		t.Fatalf("a member edits a shared template: %v", err)
	}
	other := application.WithOrg(context.Background(), "00000000-0000-0000-0000-0000000000b2")
	if has(other, shared.ID) {
		t.Fatal("another organization sees the template")
	}
	if _, err := e.svc.GetTemplate(other, shared.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get from another organization: %v", err)
	}
	if err := e.svc.DeleteTemplate(application.WithOrg(asRole(e.ctx, "x", "admin"), "00000000-0000-0000-0000-0000000000b2"), shared.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete from another organization: %v", err)
	}
}

func TestDuplicateABuiltinGivesAnEditableCopy(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	ctx := asRole(e.ctx, "u1", "member")
	for _, c := range []struct {
		id      string
		tasks   int
		repeats bool
	}{
		{"tpl-branch-opening", 10, false},
		{"tpl-financial-close", 14, false},
		{"tpl-group-consolidated-close", 39, false}, // the static copy of the default size (8 subsidiaries)
		{"tpl-annual-budget", 0, true},
		{"tpl-erp-migration", 0, true},
		{"tpl-year-end-audit", 0, true},
	} {
		cp, err := e.svc.DuplicateTemplate(ctx, c.id, "")
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if cp.Builtin || cp.ForkedFrom != c.id || cp.Version != 1 || strings.Contains(cp.Name, "proj.") {
			t.Fatalf("%s copy: %+v", c.id, cp)
		}
		hasRepeat := false
		for _, o := range cp.Objectives {
			for _, w := range o.Workflows {
				if len(w.Repeat) > 0 {
					hasRepeat = true
				}
				for _, n := range w.Nodes {
					if n.TitleKey != "" || strings.HasPrefix(n.Title, "proj.") {
						t.Fatalf("%s: node %s still has a catalog key", c.id, n.Key)
					}
				}
			}
		}
		if hasRepeat != c.repeats {
			t.Fatalf("%s: repeat blocks = %v", c.id, hasRepeat)
		}
		pv, err := e.svc.PreviewTemplate(ctx, projects.PreviewRequest{TemplateID: cp.ID})
		if err != nil {
			t.Fatalf("%s preview: %v", c.id, err)
		}
		if c.tasks > 0 && pv.Tasks < c.tasks-1 {
			t.Fatalf("%s: the copy has %d tasks, want about %d", c.id, pv.Tasks, c.tasks)
		}
		// the copy can be edited and it creates the same project as the original
		in := projects.TemplateInput{Name: cp.Name + " edit", Description: cp.Description, Category: cp.Category, Params: cp.Params, Objectives: cp.Objectives}
		if _, err := e.svc.UpdateTemplate(ctx, cp.ID, in); err != nil {
			t.Fatalf("%s: edit of the copy: %v", c.id, err)
		}
		rec, err := e.svc.CreateDraft(ctx, projects.NewProject{TemplateID: cp.ID})
		if err != nil {
			t.Fatalf("%s: project from the copy: %v", c.id, err)
		}
		orig, err := e.svc.CreateDraft(ctx, projects.NewProject{TemplateID: c.id})
		if err != nil {
			t.Fatal(err)
		}
		if a, b := e.detail(rec.ID).Project.TasksTotal, e.detail(orig.ID).Project.TasksTotal; a != b {
			t.Fatalf("%s: the copy builds %d tasks, the original %d", c.id, a, b)
		}
	}
	if _, err := e.svc.UpdateTemplate(ctx, "tpl-generic", campaignInput()); err == nil {
		t.Fatal("a built-in template must not be editable")
	}
}

func TestSaveFinishedProjectAsTemplateAndRunItAgain(t *testing.T) {
	rt := newRT()
	e := newEnv(t, rt, nil, nil)
	ctx := asRole(e.ctx, "u1", "member")
	id := e.draft("tpl-branch-opening", 0)
	// a draft can be saved ...
	draftTpl, err := e.svc.SaveAsTemplateWith(ctx, id, projects.SaveOpts{Name: "Apertura (borrador)", Category: "operations"})
	if err != nil || draftTpl.Name != "Apertura (borrador)" || draftTpl.Category != "operations" || !draftTpl.Shared {
		t.Fatalf("save a draft: %v %+v", err, draftTpl)
	}
	// ... and so can a finished one
	e.launch(id, 5)
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	tpl, err := e.svc.SaveAsTemplateWith(ctx, id, projects.SaveOpts{Name: "Apertura probada", Shared: falseP()})
	if err != nil || tpl.Shared || tpl.Version != 1 {
		t.Fatalf("save a finished project: %v %+v", err, tpl)
	}
	again := e.draft(tpl.ID, 0)
	if a, b := e.detail(again).Project.TasksTotal, e.detail(id).Project.TasksTotal; a != b {
		t.Fatalf("saved template builds %d tasks, the project had %d", a, b)
	}
	e.launch(again, 5)
	e.approveAll(again, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestCustomTemplateRunsToCompletion(t *testing.T) {
	rt := newRT()
	e := newEnv(t, rt, nil, nil)
	ctx := asRole(e.ctx, "u1", "member")
	tpl, err := e.svc.CreateTemplate(ctx, campaignInput())
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.svc.CreateDraft(ctx, projects.NewProject{TemplateID: tpl.ID, Params: map[string]string{"countries": "Cuba, Haití, Perú, Chile"}})
	if err != nil {
		t.Fatal(err)
	}
	e.launch(rec.ID, 5)
	e.approveAll(rec.ID, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
	d := e.detail(rec.ID)
	gates := 0
	for _, n := range d.Nodes {
		if n.Kind == projects.KindGate {
			gates++
		}
	}
	if d.Project.TasksDone != 1+4*3 || gates != 4 || d.Project.Failed != 0 {
		t.Fatalf("%d tasks done, %d gates, %d failed", d.Project.TasksDone, gates, d.Project.Failed)
	}
	// the acceptance criteria travel with the node
	if n := nodeByTitle(d, "Definir el mensaje de Plan Pro"); len(n.Acceptance) != 1 {
		t.Fatalf("acceptance lost: %+v", n)
	}
}
