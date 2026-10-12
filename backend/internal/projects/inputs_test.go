package projects_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/demodata"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// H1: templates declare the data they need; launch waits for it.

func builtinsWithInputs(t *testing.T) []projects.Template {
	t.Helper()
	e := newEnv(t, newRT(), nil, nil)
	all, err := e.svc.Templates(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []projects.Template
	for _, tp := range all {
		if tp.Builtin && len(tp.Inputs) > 0 {
			out = append(out, tp)
		}
	}
	return out
}

func TestLargeBuiltinTemplatesDeclareInputsAndASampleDataset(t *testing.T) {
	have := map[string]projects.Template{}
	for _, tp := range builtinsWithInputs(t) {
		have[tp.Key] = tp
	}
	for _, key := range []string{"group_consolidated_close", "acquisition_due_diligence", "market_expansion", "quarterly_cash_close", "annual_budget",
		"erp_migration", "compliance_program", "product_launch", "post_merger_integration", "year_end_audit"} {
		tp, ok := have[key]
		if !ok {
			t.Errorf("%s declares no inputs", key)
			continue
		}
		required := 0
		for _, in := range tp.Inputs {
			if in.Required {
				required++
			}
			if in.Label == "" || in.Description == "" || in.Kind == "" {
				t.Errorf("%s/%s: label, description and kind are required: %+v", key, in.Key, in)
			}
		}
		if required == 0 {
			t.Errorf("%s: a large template needs at least one required input", key)
		}
		if tp.DemoSet == "" || !demodata.Has(tp.DemoSet) {
			t.Errorf("%s: no sample dataset (%q)", key, tp.DemoSet)
		}
	}
}

// The sample dataset must satisfy every file input of its template, at the defaults and at the maximum size.
func TestSampleDatasetSatisfiesTheRequiredInputsAtAnySize(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	sizes := map[string]map[string]string{
		"group_consolidated_close":  {"subsidiaries": "20"},
		"acquisition_due_diligence": {"entities": "10"},
		"market_expansion":          {"countries": "12"},
		"quarterly_cash_close":      {"accounts": "12", "clients": "3"},
		"annual_budget":             {"units": "A, B, C, D", "countries": "X, Y, Z"},
		"year_end_audit":            {"entities": "E1, E2, E3, E4, E5, E6, E7, E8"},
		"post_merger_integration":   {"entities": "A, B, C, D, E, F"},
	}
	for _, tp := range builtinsWithInputs(t) {
		for _, params := range []map[string]string{nil, sizes[tp.Key]} {
			rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: tp.ID, Params: params, Locale: "es"})
			if err != nil {
				t.Fatalf("%s: %v", tp.Key, err)
			}
			if rec.DemoParams == nil || rec.DemoSet == "" {
				t.Fatalf("%s: the draft has no demo parameters", tp.Key)
			}
			set, err := demodata.Generate(rec.DemoSet, *rec.DemoParams)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			for _, f := range set.Files {
				got[f.Input]++
			}
			for _, in := range rec.Inputs {
				switch in.Kind {
				case projects.InputFile, projects.InputFolder:
					need := 1
					if in.Kind == projects.InputFolder && in.MinFiles > 0 {
						need = in.MinFiles
					}
					if got[in.Key] < need {
						t.Errorf("%s %v: input %s needs %d files, the sample has %d", tp.Key, params, in.Key, need, got[in.Key])
					}
					for _, f := range set.Files {
						if f.Input == in.Key && !in.AcceptsName(f.Name) {
							t.Errorf("%s: sample file %s is not accepted by input %s %v", tp.Key, f.Name, in.Key, in.Accept)
						}
					}
				default:
					if set.Values[in.Key] == "" && in.Required {
						t.Errorf("%s: no sample value for %s", tp.Key, in.Key)
					}
				}
			}
		}
	}
}

func TestDraftCarriesTheInputsOfItsParameters(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "group_consolidated_close", Params: map[string]string{"subsidiaries": "3"}, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	d := e.detail(rec.ID)
	if d.Inputs == nil || d.Inputs.Ready || len(d.Inputs.Missing) != 4 {
		t.Fatalf("inputs: %+v", d.Inputs)
	}
	if d.Inputs.DemoSet != demodata.GroupSet {
		t.Fatalf("demo set %q", d.Inputs.DemoSet)
	}
	for _, it := range d.Inputs.Items {
		if it.Key == "tb" && (it.MinFiles != 3 || it.Kind != projects.InputFolder) {
			t.Fatalf("tb: %+v", it.InputSpec)
		}
	}
	// a template without inputs has none
	g, _ := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "tpl-generic", Goal: "algo", Locale: "es"})
	if e.detail(g.ID).Inputs != nil {
		t.Fatal("the generic template has no inputs")
	}
}

func fillAll(t *testing.T, e *env, id string) {
	t.Helper()
	rec, _ := e.svc.Record(e.ctx, id)
	if _, err := e.svc.UpdateInputs(e.ctx, id, func(r *projects.Record) error {
		for i := range r.Inputs {
			in := &r.Inputs[i]
			if in.Kind == projects.InputFile || in.Kind == projects.InputFolder {
				for k := 0; k < max(1, in.MinFiles); k++ {
					_ = projects.AddInputFile(in, projects.InputFileRef{Path: "/Projects/x/Entradas/" + in.Key + string(rune('a'+k)) + ".csv", Name: "f.csv", Source: projects.InputSourceUpload, AddedAt: time.Now()})
				}
			}
		}
		r.InputsFolder = "/Projects/x/Entradas"
		return nil
	}); err != nil {
		t.Fatalf("%v (%+v)", err, rec.Inputs)
	}
}

func TestLaunchWaitsForTheRequiredInputs(t *testing.T) {
	rt := newRT()
	var mu sync.Mutex
	var descs []string
	rt.onRun = func(in application.RunTaskRequest) {
		mu.Lock()
		descs = append(descs, in.Task.Description)
		mu.Unlock()
	}
	e := newEnv(t, rt, nil, nil)
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "group_consolidated_close", Params: map[string]string{"subsidiaries": "1"}, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Launch(e.ctx, rec.ID, projects.LaunchBody{ApprovedBudgetUSD: 500, AcknowledgeUnderbudget: true})
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "inputs_required") || !strings.Contains(err.Error(), "fx_rates") {
		t.Fatalf("launch must wait for the inputs: %v", err)
	}
	if got := e.detail(rec.ID).Project.Status; got != "draft" {
		t.Fatalf("status %s", got)
	}
	// one input is not enough
	if _, err := e.svc.UpdateInputs(e.ctx, rec.ID, func(r *projects.Record) error {
		return projects.AddInputFile(projects.InputByKey(r, "fx_rates"), projects.InputFileRef{Path: "/Projects/x/Entradas/fx.csv", Name: "fx.csv", Source: projects.InputSourceUpload})
	}); err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Inputs(e.ctx, rec.ID)
	if v.Ready || len(v.Missing) != 3 {
		t.Fatalf("missing %v", v.Missing)
	}
	fillAll(t, e, rec.ID)
	if v, _ = e.svc.Inputs(e.ctx, rec.ID); !v.Ready {
		t.Fatalf("still missing %v", v.Missing)
	}
	if _, err := e.svc.Launch(e.ctx, rec.ID, projects.LaunchBody{ApprovedBudgetUSD: 500, AcknowledgeUnderbudget: true}); err != nil {
		t.Fatal(err)
	}
	r2, _ := e.svc.Record(e.ctx, rec.ID)
	if r2.InputsWaived != nil {
		t.Fatal("nothing was waived")
	}
	e.waitFor("a task to run", func() bool { return rt.startedCount() > 0 })
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(descs[0], "datos de entrada") || !strings.Contains(descs[0], "/Projects/x/Entradas") || strings.Contains(descs[0], "SIN todos") {
		t.Fatalf("the task must point to the inputs: %q", descs[0])
	}
	// the inputs cannot change once launched
	if _, err := e.svc.SetInputValue(e.ctx, rec.ID, "materiality", "x"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("inputs of a launched project: %v", err)
	}
}

func TestLaunchWithoutInputsIsRecordedAndTheAgentsAreTold(t *testing.T) {
	rt := newRT()
	var mu sync.Mutex
	var descs []string
	rt.onRun = func(in application.RunTaskRequest) {
		mu.Lock()
		descs = append(descs, in.Task.Description)
		mu.Unlock()
	}
	e := newEnv(t, rt, nil, nil)
	ctx := application.WithActor(e.ctx, "u-ana")
	rec, err := e.svc.CreateDraft(ctx, projects.NewProject{TemplateID: "group_consolidated_close", Params: map[string]string{"subsidiaries": "1"}, Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Launch(ctx, rec.ID, projects.LaunchBody{ApprovedBudgetUSD: 500, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}); err != nil {
		t.Fatal(err)
	}
	r2, _ := e.svc.Record(ctx, rec.ID)
	if r2.InputsWaived == nil || r2.InputsWaived.By != "u-ana" || len(r2.InputsWaived.Missing) != 4 {
		t.Fatalf("waiver: %+v", r2.InputsWaived)
	}
	if v := e.detail(rec.ID).Inputs; v == nil || v.Waived == nil || v.Ready {
		t.Fatalf("detail must show the waiver: %+v", v)
	}
	e.waitFor("a task to run", func() bool { return rt.startedCount() > 0 })
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(descs[0], "WITHOUT all the required input data") || !strings.Contains(descs[0], "ASSUMPTION") {
		t.Fatalf("the agents must be told: %q", descs[0])
	}
}

func TestOptionalOnlyInputsNeverBlockLaunch(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "financial_close", Goal: "Cierre", Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Inputs(e.ctx, rec.ID)
	if len(v.Items) != 1 || !v.Ready {
		t.Fatalf("%+v", v)
	}
	e.launch(rec.ID, 5)
}

func TestInputValuesAreValidated(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "erp_migration", Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetInputValue(e.ctx, rec.ID, "program_budget", "mucho"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a number input must be a number: %v", err)
	}
	v, err := e.svc.SetInputValue(e.ctx, rec.ID, "program_budget", "2400000")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range v.Items {
		if it.Key == "program_budget" && !it.Satisfied {
			t.Fatal("a valid number satisfies the input")
		}
	}
	if _, err := e.svc.SetInputValue(e.ctx, rec.ID, "systems", "x"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a file input takes no value: %v", err)
	}
	if _, err := e.svc.SetInputValue(e.ctx, rec.ID, "nope", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown input: %v", err)
	}
	// an unfilled one-file input accepts only the extensions it declares
	spec := projects.InputSpec{Accept: []string{"csv", "xlsx"}}
	if !spec.AcceptsName("A.CSV") || spec.AcceptsName("a.exe") || spec.AcceptsName("csv") {
		t.Fatal("accept check")
	}
}

func inputsTemplate() projects.TemplateInput {
	in := campaignInput()
	in.Inputs = &[]projects.InputSpec{
		{Key: "brief", Label: "Brief", Description: "El brief de la campaña", Kind: projects.InputFile, Accept: []string{".MD", "pdf"}, Required: true, Example: "brief.md"},
		{Key: "audiences", Label: "Audiencias", Kind: projects.InputFolder, PerParam: "countries", Required: true},
		{Key: "tone", Label: "Tono", Kind: projects.InputSelect, Options: []string{"formal", "cercano"}},
	}
	return in
}

func TestCustomTemplatesDeclareValidatedInputsAndKeepThemAcrossVersions(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	member := asRole(e.ctx, "u-member", "member")
	for name, mut := range map[string]func(*projects.TemplateInput){
		"bad key":      func(in *projects.TemplateInput) { (*in.Inputs)[0].Key = "Bad Key" },
		"duplicate":    func(in *projects.TemplateInput) { (*in.Inputs)[1].Key = "brief" },
		"bad kind":     func(in *projects.TemplateInput) { (*in.Inputs)[0].Kind = "blob" },
		"no label":     func(in *projects.TemplateInput) { (*in.Inputs)[0].Label = "" },
		"select":       func(in *projects.TemplateInput) { (*in.Inputs)[2].Options = nil },
		"accept":       func(in *projects.TemplateInput) { (*in.Inputs)[0].Accept = []string{"a b"} },
		"unknown prm":  func(in *projects.TemplateInput) { (*in.Inputs)[1].PerParam = "ghost" },
		"files on txt": func(in *projects.TemplateInput) { (*in.Inputs)[2].MinFiles = 2 },
	} {
		in := inputsTemplate()
		mut(&in)
		if _, err := e.svc.CreateTemplate(member, in); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), projects.IssueBadInput) {
			t.Errorf("%s: must be rejected as invalid_input: %v", name, err)
		}
	}
	tpl, err := e.svc.CreateTemplate(member, inputsTemplate())
	if err != nil || len(tpl.Inputs) != 3 || tpl.Inputs[0].Accept[0] != "md" {
		t.Fatalf("create: %v %+v", err, tpl.Inputs)
	}
	// editing the structure without sending the inputs keeps them; [] clears them
	edit := campaignInput()
	edit.Description = "otra"
	v2, err := e.svc.UpdateTemplate(member, tpl.ID, edit)
	if err != nil || len(v2.Inputs) != 3 || v2.Version != 2 {
		t.Fatalf("update: %v %+v", err, v2.Inputs)
	}
	vs, _ := e.svc.TemplateVersions(member, tpl.ID)
	old, err := e.svc.RestoreTemplate(member, tpl.ID, 1)
	if err != nil || len(old.Inputs) != 3 || len(vs) != 2 {
		t.Fatalf("restore: %v %+v", err, old.Inputs)
	}
	empty := campaignInput()
	empty.Inputs = &[]projects.InputSpec{}
	if v4, err := e.svc.UpdateTemplate(member, tpl.ID, empty); err != nil || len(v4.Inputs) != 0 {
		t.Fatalf("clear: %v %+v", err, v4.Inputs)
	}
	// a project from the template turns them into inputs sized by the list parameter
	if _, err := e.svc.UpdateTemplate(member, tpl.ID, inputsTemplate()); err != nil {
		t.Fatal(err)
	}
	rec, err := e.svc.CreateDraft(member, projects.NewProject{TemplateID: tpl.ID, Params: map[string]string{"countries": "México, Chile"}, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Inputs(member, rec.ID)
	if len(v.Items) != 3 || v.Items[1].MinFiles != 2 || len(v.Missing) != 2 {
		t.Fatalf("%+v", v)
	}
}

func TestDuplicatingABuiltinKeepsItsInputs(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	cp, err := e.svc.DuplicateTemplate(asRole(e.ctx, "u-member", "member"), "group_consolidated_close", "Mi cierre")
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Inputs) != 5 || cp.DemoSet != demodata.GroupSet {
		t.Fatalf("copy lost its inputs: %+v %q", cp.Inputs, cp.DemoSet)
	}
	for _, in := range cp.Inputs {
		if in.PerParam != "" || in.LabelKey != "" || in.Label == "" {
			t.Fatalf("the copy must be plain text without references to dropped parameters: %+v", in)
		}
		if in.Key == "tb" && in.MinFiles != 8 {
			t.Fatalf("one trial balance per subsidiary at the default size: %+v", in)
		}
	}
	// and a project made from the copy asks for the same data
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: cp.ID, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.Inputs(e.ctx, rec.ID); len(v.Missing) != 4 {
		t.Fatalf("%+v", v.Missing)
	}
}
