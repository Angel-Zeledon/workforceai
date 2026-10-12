package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func TestStandingEligibilityIsDefaultDeny(t *testing.T) {
	cases := []struct {
		name                     string
		tool, action, risk, rule string
		required                 int
		role                     string
		want                     bool
		wantWhy                  string
	}{
		{"internal draft by autonomy", "documents", "draft", "low", "baseline.autonomy:approve_each", 1, "", true, ""},
		{"spreadsheet update by engine autonomy", "spreadsheet", "update", "medium", "autonomy.approve_each", 1, "", true, ""},
		{"drive versioned write", "fs", "write", "medium", "native.autonomy", 1, "", true, ""},
		{"drive delete", "fs", "delete", "medium", "native.autonomy", 1, "", false, "not_internal_reversible"},
		{"email send", "email", "send_proposal", "medium", "baseline.autonomy:approve_each", 1, "", false, "not_internal_reversible"},
		{"external verb on an internal tool", "documents", "send_contract", "medium", "baseline.autonomy:approve_each", 1, "", false, "not_internal_reversible"},
		{"publish", "documents", "publish", "low", "baseline", 1, "", false, "not_internal_reversible"},
		{"payment", "spreadsheet", "make_payment", "low", "baseline", 1, "", false, "not_internal_reversible"},
		{"unknown tool", "crm", "update_deal", "low", "baseline", 1, "", false, "not_internal_reversible"},
		{"code.run by tool and action", "code", "run", "low", "baseline", 1, "", false, "code_run_needs_each_approval"},
		{"code.run spelled as one action", "documents", "code.run", "low", "baseline", 1, "", false, "code_run_needs_each_approval"},
		{"high risk", "documents", "draft", "high", "baseline", 1, "", false, "risk_too_high"},
		{"missing risk", "documents", "draft", "", "baseline", 1, "", false, "risk_too_high"},
		{"always-approve list", "documents", "draft", "low", "baseline.approval_action:draft", 1, "", false, "organization_rule"},
		{"amount threshold", "spreadsheet", "update", "low", "org.amount", 1, "", false, "organization_rule"},
		{"tainted native hold", "fs", "write", "medium", "native.restricted", 1, "", false, "organization_rule"},
		{"double approval", "documents", "draft", "low", "baseline", 2, "", false, "governance_required"},
		{"role required", "documents", "draft", "low", "baseline", 1, "admin", false, "governance_required"},
	}
	for _, c := range cases {
		got, why := application.StandingEligibility(c.tool, c.action, c.risk, c.rule, c.required, c.role)
		if got != c.want || why != c.wantWhy {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, got, why, c.want, c.wantWhy)
		}
	}
}

type fakeResolver struct{ active map[string]bool }

func (f fakeResolver) ProjectOfRequest(context.Context, string, string) (string, bool) {
	return "proj1", f.active["proj1"]
}
func (f fakeResolver) ProjectActive(_ context.Context, _, id string) bool { return f.active[id] }

// stubGate lets every task run normally; its project resolver says every request belongs to proj1.
type stubGate struct{ fakeResolver }

func (stubGate) GateTask(context.Context, string, domain.Task) application.GateDecision {
	return application.GateDecision{}
}
func (stubGate) GateResolved(context.Context, string, domain.Task, string, bool) {}

func newStanding(h *harness, res fakeResolver) *application.StandingService {
	return &application.StandingService{Store: h.store, Core: h.store, Projects: res, Rec: &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub},
		Cfg: application.DefaultConfig(), Days: 7}
}

// standingToolRuntime makes the first task of the plan ask for one tool action.
func standingToolRuntime(tool, action, risk string) *fakeRuntime {
	return &fakeRuntime{plan: proposalPlan(), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho: " + in.Task.Title)
		if in.Task.Title == "Preparar propuesta" {
			res.ToolRequests = []application.ToolRequest{{Tool: tool, Action: action, Risk: risk, Args: map[string]any{"cell": "B2"}}}
		}
		return res, nil
	}}
}

func standingHarness(t *testing.T, tool, action, risk string) (*harness, *application.StandingService, fakeResolver) {
	h := newHarness(t, standingToolRuntime(tool, action, risk), nil)
	if err := h.store.SetAgentAutonomy(context.Background(), domain.DemoOrgID, "sales", "approve_each"); err != nil {
		t.Fatal(err)
	}
	res := fakeResolver{active: map[string]bool{"proj1": true}}
	svc := newStanding(h, res)
	h.orch.SetStanding(svc)
	h.orch.SetTaskGate(stubGate{res})
	return h, svc, res
}

func approvalsOf(h *harness) []domain.Approval {
	all, _ := h.store.ListApprovals(context.Background(), domain.DemoOrgID, "")
	return all
}

func TestStandingApprovalSkipsTheHumanForInternalActionsOnly(t *testing.T) {
	h, svc, _ := standingHarness(t, "spreadsheet", "update", "medium")
	ctx := context.Background()

	// First time: a human is asked, and the card can offer "always allow".
	if _, err := h.orch.Submit(ctx, "actualiza la hoja"); err != nil {
		t.Fatal(err)
	}
	ap := h.awaitApproval()
	if ap.Tool != "spreadsheet" || ap.ProjectID != "proj1" || !ap.StandingEligible {
		t.Fatalf("approval = %+v", ap)
	}
	if _, err := h.appr.Decide(ctx, ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if st.ProjectID != "proj1" || st.AgentID != "sales" || st.Tool != "spreadsheet" || st.Action != "update" || st.Status != "active" {
		t.Fatalf("standing = %+v", st)
	}
	if !st.ExpiresAt.After(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("expiry = %v", st.ExpiresAt)
	}
	h.waitFor("first request done", func() bool { return h.q != nil && countStatus(h, domain.ApprovalApproved) == 1 })

	// Second time: nobody is asked; an auto-approved approval records who decided and why.
	before := len(approvalsOf(h))
	if _, err := h.orch.Submit(ctx, "actualiza la hoja otra vez"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("auto approval", func() bool { return len(approvalsOf(h)) == before+1 })
	if _, pending := h.pendingApproval(); pending {
		t.Fatal("a standing approval must not leave a pending approval")
	}
	var auto domain.Approval
	for _, a := range approvalsOf(h) {
		if a.ID != ap.ID {
			auto = a
		}
	}
	if auto.Status != domain.ApprovalApproved || len(auto.Decisions) != 1 || auto.Decisions[0].By != "standing:"+st.ID {
		t.Fatalf("auto approval = %+v", auto)
	}
	h.waitFor("audit", func() bool { return h.hasAudit("approval.auto_approved") && h.hasAudit("standing_approval.used") })
	if got, _ := svc.List(ctx, "proj1"); len(got) != 1 || got[0].UseCount != 1 {
		t.Fatalf("list = %+v", got)
	}

	// Revoked: the human is asked again.
	if _, err := svc.Revoke(ctx, "proj1", st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.orch.Submit(ctx, "otra vez"); err != nil {
		t.Fatal(err)
	}
	h.awaitApproval()
}

func countStatus(h *harness, st domain.ApprovalStatus) int {
	n := 0
	for _, a := range approvalsOf(h) {
		if a.Status == st {
			n++
		}
	}
	return n
}

func TestStandingNeverAppliesToExternalOrIrreversibleActions(t *testing.T) {
	for _, c := range []struct{ tool, action, risk string }{
		{"email", "send_proposal", "medium"},
		{"documents", "publish", "low"},
		{"spreadsheet", "make_payment", "low"},
		{"fs", "delete", "medium"},
		{"documents", "draft", "high"},
	} {
		h, svc, _ := standingHarness(t, c.tool, c.action, c.risk)
		ctx := context.Background()
		if _, err := h.orch.Submit(ctx, "haz "+c.action); err != nil {
			t.Fatal(err)
		}
		ap := h.awaitApproval()
		if ap.StandingEligible {
			t.Errorf("%s.%s must not be offered the always-allow choice", c.tool, c.action)
		}
		if _, err := h.appr.Decide(ctx, ap.ID, "approve", ""); err != nil {
			t.Fatal(err)
		}
		// Even asking the server directly (a forged client) is refused.
		if _, err := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s.%s: grant = %v, want ErrInvalid", c.tool, c.action, err)
		}
		// And a stored row that should not exist is ignored when matching.
		if _, ok := svc.Match(ctx, "proj1", "sales", c.tool, c.action, c.risk, "baseline", 1, ""); ok {
			t.Errorf("%s.%s matched", c.tool, c.action)
		}
	}
}

func TestStandingExpiresRevokesAndEndsWithTheProject(t *testing.T) {
	h, svc, res := standingHarness(t, "spreadsheet", "update", "medium")
	ctx := context.Background()
	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }
	ap, _, err := h.appr.Request(ctx, domain.Approval{TaskID: "t1", AgentID: "sales", Action: "update", Tool: "spreadsheet", ProjectID: "proj1",
		StandingEligible: true, Risk: "medium", PolicyRule: "baseline.autonomy:approve_each"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("grant on a pending approval = %v", err)
	}
	if _, err := h.appr.Decide(ctx, ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID, Days: 2})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID, Days: 2})
	if again.ID != st.ID {
		t.Error("the same scope twice must not pile up rows")
	}
	match := func() bool {
		_, ok := svc.Match(ctx, "proj1", "sales", "spreadsheet", "update", "medium", "baseline.autonomy:approve_each", 1, "")
		return ok
	}
	if !match() {
		t.Fatal("fresh grant must match")
	}
	if _, ok := svc.Match(ctx, "proj1", "hr", "spreadsheet", "update", "medium", "baseline", 1, ""); ok {
		t.Error("a grant for one agent must not cover another")
	}
	if _, ok := svc.Match(ctx, "proj2", "sales", "spreadsheet", "update", "medium", "baseline", 1, ""); ok {
		t.Error("a grant must not leak to another project")
	}
	if _, ok := svc.Match(ctx, "proj1", "sales", "spreadsheet", "append", "medium", "baseline", 1, ""); ok {
		t.Error("a grant must not cover another action")
	}
	// Ends with the project.
	res.active["proj1"] = false
	if match() {
		t.Error("a finished project must end its standing approvals")
	}
	res.active["proj1"] = true
	// Expires after its days.
	now = now.Add(3 * 24 * time.Hour)
	if match() {
		t.Error("an expired grant matched")
	}
	if got, _ := svc.List(ctx, "proj1"); got[0].Status != "expired" {
		t.Errorf("status = %s", got[0].Status)
	}
	// Revocation (idempotent, scoped to the project).
	now = now.Add(-3 * 24 * time.Hour)
	if _, err := svc.Revoke(ctx, "other", st.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke from another project = %v", err)
	}
	r1, err := svc.Revoke(ctx, "proj1", st.ID)
	if err != nil || r1.Status != "revoked" {
		t.Fatalf("revoke = %+v %v", r1, err)
	}
	if r2, _ := svc.Revoke(ctx, "proj1", st.ID); r2.RevokedAt == nil || !r2.RevokedAt.Equal(*r1.RevokedAt) {
		t.Error("revoking twice must keep the first revocation")
	}
	if match() {
		t.Error("a revoked grant matched")
	}
	if !h.hasAudit("standing_approval.created") || !h.hasAudit("standing_approval.revoked") {
		t.Error("creation and revocation must be audited")
	}
}

func TestStandingAnyAgentWidensTheScope(t *testing.T) {
	h, svc, _ := standingHarness(t, "documents", "draft", "low")
	ctx := context.Background()
	ap, _, _ := h.appr.Request(ctx, domain.Approval{TaskID: "t1", AgentID: "sales", Action: "draft", Tool: "documents", ProjectID: "proj1",
		StandingEligible: true, Risk: "low", PolicyRule: "baseline"})
	_, _ = h.appr.Decide(ctx, ap.ID, "approve", "")
	st, err := svc.Grant(ctx, application.StandingGrantInput{ApprovalID: ap.ID, AnyAgent: true})
	if err != nil || st.AgentID != "" {
		t.Fatalf("grant = %+v %v", st, err)
	}
	if _, ok := svc.Match(ctx, "proj1", "legal", "documents", "draft", "low", "baseline", 1, ""); !ok {
		t.Error("an any-agent grant must cover every agent of the project")
	}
}
