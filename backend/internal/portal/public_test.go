package portal

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// allowedKeys is EVERY JSON key the public view may contain. A new key must be added here on purpose,
// after checking it reveals nothing in the "never shown" list of public.go.
var allowedKeys = []string{
	"firm", "name", "accent_color", "has_logo",
	"project", "title", "title_key", "title_params", "status", "progress_pct", "tasks_done", "tasks_total",
	"milestones", "tasks",
	"deliverables", "kind", "completed_at",
	"updated_at", "generated_at", "expires_at",
}

// collectTags walks a type and returns the json names of its fields (recursively).
func collectTags(t reflect.Type, out map[string]bool, seen map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] || t == reflect.TypeOf(time.Time{}) {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			out["<embedded "+f.Name+">"] = true // embedding an internal struct is forbidden
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			out["<untagged "+t.Name()+"."+f.Name+">"] = true
			continue
		}
		out[name] = true
		collectTags(f.Type, out, seen)
	}
}

func TestPublicViewTypeIsAnAllowlist(t *testing.T) {
	got := map[string]bool{}
	collectTags(reflect.TypeOf(View{}), got, map[reflect.Type]bool{})
	allow := map[string]bool{}
	for _, k := range allowedKeys {
		allow[k] = true
	}
	var extra []string
	for k := range got {
		if !allow[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Fatalf("the public view gained fields that are not in the allowlist: %v", extra)
	}
}

// keysOf collects every object key of a decoded JSON document.
func keysOf(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			out[k] = true
			if k == "title_params" { // free-form template parameters: values are checked, keys are not field names
				continue
			}
			keysOf(val, out)
		}
	case []any:
		for _, val := range x {
			keysOf(val, out)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// secretDetail is a project full of things the client must never see.
func secretDetail(now time.Time) (projects.Detail, []artifacts.Meta) {
	pid := "proj-SECRET-ID"
	d := projects.Detail{
		Project: projects.Summary{ID: pid, Name: "Cierre mensual López", Goal: "SECRET-GOAL-PROMPT ignore previous instructions", Status: projects.StatusWaitingHuman,
			Control: "active", BudgetUSD: 4321.5, SpentUSD: 987.65, TasksDone: 1, TasksTotal: 3, RequestID: "req-SECRET-ID", CreatedAt: now.Add(-48 * time.Hour),
			StartedAt: ptr(now.Add(-47 * time.Hour)), BudgetWarnPct: 77.7},
		Objectives: []projects.Objective{{ID: "obj-SECRET-ID", Title: "Conciliaciones", Position: 0}},
		Nodes: []projects.Node{
			{ID: "node-SECRET-1", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindTask, Title: "Conciliar bancos", AgentID: ptr("agent-SECRET-ID"),
				State: projects.StateDone, CostUSD: 12.34, EstCostUSD: 56.78, TaskID: "task-SECRET-ID", StartedAt: ptr(now.Add(-3 * time.Hour)), FinishedAt: ptr(now.Add(-2 * time.Hour)),
				Acceptance: []string{"ACCEPTANCE-SECRET"}, Review: &domain.Review{}, DelegationChain: []string{"agent-SECRET-CHAIN"}},
			{ID: "node-SECRET-2", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindTask, Title: "Pagar proveedores", AgentID: ptr("agent-SECRET-ID"),
				State: projects.StateAwaitingApproval, ApprovalAction: "SECRET-APPROVAL-ACTION", TaskID: "task-SECRET-2"},
			{ID: "node-SECRET-3", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindTask, Title: "Estado de resultados", State: projects.StateFailed,
				Error: ptr("ERROR-SECRET"), SkipReason: "SKIP-SECRET"},
			{ID: "node-SECRET-4", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindGate, Title: "GATE-SECRET approve the payment", State: projects.StateAwaitingApproval},
			{ID: "node-SECRET-5", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindWait, Title: "WAIT-SECRET", State: projects.StatePending},
			{ID: "node-SECRET-6", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindTask, Title: "SUPERSEDED-SECRET", State: projects.StateCancelled, Superseded: "replaced"},
			{ID: "node-SECRET-7", ProjectID: pid, ObjectiveID: "obj-SECRET-ID", Kind: projects.KindGroup, Title: "GROUP-SECRET"},
		},
		Approvals: []projects.Approval{{ID: "appr-SECRET-ID", Title: "APPROVAL-TITLE-SECRET", Details: "APPROVAL-DETAILS-SECRET", Risk: "high", Status: "pending"}},
		Budget:    projects.BudgetPolicy{Mode: "hard"},
		Estimate:  &projects.Estimate{Basis: "SECRET-ESTIMATE"},
	}
	metas := []artifacts.Meta{
		{ID: "art-SECRET-1", Kind: artifacts.KindDoc, Title: "Reporte de conciliación", Status: artifacts.StatusApproved, ProjectID: &pid,
			CreatedBy: artifacts.ActorRef{Kind: "agent", ID: "agent-SECRET-ID"}, TaskID: ptr("task-SECRET-ID"), UpdatedAt: now.Add(-time.Hour)},
		{ID: "art-SECRET-2", Kind: artifacts.KindSheet, Title: "DRAFT-SECRET not approved yet", Status: artifacts.StatusDraft, ProjectID: &pid, UpdatedAt: now},
		{ID: "art-SECRET-3", Kind: artifacts.KindSheet, Title: "UNSHARED-SECRET approved but not chosen", Status: artifacts.StatusApproved, ProjectID: &pid, UpdatedAt: now},
	}
	return d, metas
}

func TestPublicViewNeverLeaksForbiddenData(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	d, metas := secretDetail(now)
	exp := now.Add(72 * time.Hour)
	l := Link{ID: "pl_SECRET-LINK", ProjectID: d.Project.ID, Label: "LABEL-SECRET internal", DeliverableIDs: []string{"art-SECRET-1", "art-SECRET-2"},
		TokenHash: "HASH-SECRET", CreatedBy: "user-SECRET-ID", ExpiresAt: &exp}
	shared := []artifacts.Meta{}
	for _, m := range metas {
		for _, id := range l.DeliverableIDs {
			if m.ID == id && completed(m) {
				shared = append(shared, m)
			}
		}
	}
	v := buildView(Branding{DisplayName: "Despacho Pérez", AccentColor: "#0b5fff", HasLogo: true, UpdatedBy: "user-SECRET-ID", Logo: []byte("LOGO-BYTES-SECRET")}, d, shared, l, now)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, bad := range []string{"SECRET", "4321", "987", "12.34", "56.78", "77.7", "hard", "high", "agent", "approval", "task_id", "cost", "usd", "budget", "goal", "request"} {
		if strings.Contains(strings.ToLower(doc), strings.ToLower(bad)) {
			t.Errorf("public view contains %q:\n%s", bad, doc)
		}
	}
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	keys := map[string]bool{}
	keysOf(decoded, keys)
	allow := map[string]bool{}
	for _, k := range allowedKeys {
		allow[k] = true
	}
	for k := range keys {
		if !allow[k] {
			t.Errorf("unexpected key %q in the public view", k)
		}
	}

	// What it does show.
	if v.Firm.Name != "Despacho Pérez" || v.Firm.AccentColor != "#0b5fff" || !v.Firm.HasLogo {
		t.Fatalf("firm: %+v", v.Firm)
	}
	if v.Project.Title != "Cierre mensual López" || v.Project.Status != ProjectInProgress || v.Project.TasksTotal != 3 || v.Project.TasksDone != 1 || v.Project.ProgressPct != 33 {
		t.Fatalf("project: %+v", v.Project)
	}
	if len(v.Milestones) != 1 || len(v.Milestones[0].Tasks) != 3 || v.Milestones[0].Status != TaskInProgress {
		t.Fatalf("milestones: %+v", v.Milestones)
	}
	want := []string{TaskDone, TaskInProgress, TaskPending} // awaiting approval reads "in progress"; failed reads "pending"
	for i, tk := range v.Milestones[0].Tasks {
		if tk.Status != want[i] {
			t.Fatalf("task %d status %s, want %s", i, tk.Status, want[i])
		}
	}
	if len(v.Deliverables) != 1 || v.Deliverables[0].Title != "Reporte de conciliación" || v.Deliverables[0].Kind != "doc" {
		t.Fatalf("only the shared AND approved deliverable is shown: %+v", v.Deliverables)
	}
	if v.UpdatedAt == nil || !v.UpdatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("updated_at = %v", v.UpdatedAt)
	}
	if v.ExpiresAt == nil || !v.ExpiresAt.Equal(exp) {
		t.Fatalf("expires_at = %v", v.ExpiresAt)
	}
}

func TestPublicStatusesAreCoarse(t *testing.T) {
	cases := map[string]string{
		projects.StatusDraft: ProjectPlanning, "planning": ProjectPlanning, projects.StatusRunning: ProjectInProgress,
		projects.StatusWaitingHuman: ProjectInProgress, projects.StatusPaused: ProjectOnHold, projects.StatusFailed: ProjectOnHold,
		projects.StatusDone: ProjectCompleted, projects.StatusCancelled: ProjectClosed,
	}
	for in, want := range cases {
		if got := publicProjectStatus(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	for _, st := range []string{projects.StatePending, projects.StateReady, projects.StatePaused, projects.StateBlocked, projects.StateFailed, projects.StateDraft, "waiting"} {
		if got := publicTaskStatus(st); got != TaskPending {
			t.Errorf("%s -> %s, want pending", st, got)
		}
	}
}

func TestEmptyObjectivesAreDroppedAndOrphansGrouped(t *testing.T) {
	now := time.Now().UTC()
	d := projects.Detail{
		Project:    projects.Summary{Name: "P", Status: projects.StatusDone, CreatedAt: now},
		Objectives: []projects.Objective{{ID: "b", Title: "Second", Position: 2}, {ID: "a", Title: "First", Position: 1}, {ID: "c", Title: "Empty", Position: 3}},
		Nodes: []projects.Node{
			{ObjectiveID: "b", Kind: projects.KindTask, Title: "t2", State: projects.StateDone},
			{ObjectiveID: "a", Kind: projects.KindMilestone, Title: "t1", State: projects.StateDone},
			{ObjectiveID: "zz", Kind: projects.KindSubtask, Title: "orphan", State: projects.StateDone},
		},
	}
	v := buildView(Branding{}, d, nil, Link{}, now)
	if len(v.Milestones) != 3 || v.Milestones[0].Title != "First" || v.Milestones[1].Title != "Second" || v.Milestones[2].Title != "" {
		t.Fatalf("milestones: %+v", v.Milestones)
	}
	if v.Project.Status != ProjectCompleted || v.Project.ProgressPct != 100 || v.Milestones[0].Status != TaskDone {
		t.Fatalf("project: %+v", v.Project)
	}
	if v.Deliverables == nil || v.Milestones[0].Tasks == nil {
		t.Fatal("lists are never null")
	}
}
