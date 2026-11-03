package application_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

func permService(t *testing.T) (*application.PermissionsService, *harness) {
	t.Helper()
	h := newHarness(t, toolRuntime(), nil)
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}
	svc := &application.PermissionsService{Store: h.store, Agents: h.store, Rec: rec, OrgID: domain.DemoOrgID,
		SetLevel: func(ctx context.Context, agentID, autonomy, _ string) error {
			return h.store.SetAgentAutonomy(ctx, domain.DemoOrgID, agentID, autonomy)
		},
		IsTeamLead: func(_ context.Context, _, userID, agentID string) bool {
			return userID == "lead-1" && agentID == "sales"
		}}
	return svc, h
}

func TestWhoMayChangePermissions(t *testing.T) {
	svc, h := permService(t)
	if _, err := svc.ChooseLevel(as("member-1", "member"), "sales", application.LevelImportant); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member who is not responsible for the team: %v", err)
	}
	if _, err := svc.ChooseLevel(as("agent:sales", "owner"), "sales", application.LevelAskAll); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an agent never changes permissions: %v", err)
	}
	v, err := svc.ChooseLevel(as("lead-1", "member"), "sales", application.LevelImportant)
	if err != nil || !v.Factory || v.Level != application.LevelImportant {
		t.Fatalf("the team's responsible person may: %+v %v", v, err)
	}
	if _, err := svc.ChooseLevel(as("lead-1", "member"), "legal", application.LevelAskAll); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("only for the agents of their team: %v", err)
	}
	if _, err := svc.LiftFloor(as("admin-1", "admin"), "sales", policy.KindPayments, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an admin cannot lift the floor: %v", err)
	}
	if _, err := svc.LiftFloor(as("owner-1", "owner"), "sales", policy.KindPayments, false); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("the owner must confirm the risk: %v", err)
	}
	if _, err := svc.LiftFloor(as("owner-1", "owner"), "sales", policy.KindMessages, true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("messages are not on the floor: %v", err)
	}
	v, err = svc.LiftFloor(as("owner-1", "owner"), "sales", policy.KindPayments, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Categories {
		if c.Category == policy.KindPayments && (c.Lifted == nil || c.Lifted.By != "owner-1") {
			t.Fatalf("the lift is visible with who did it: %+v", c)
		}
	}
	if e := h.auditEntries("permissions.floor_lifted"); len(e) != 1 || e[0].Actor != "owner-1" {
		t.Fatalf("lifting the floor is audited: %+v", e)
	}
	if e := h.auditEntries("permissions.refused"); len(e) < 3 {
		t.Fatalf("refusals are audited: %d", len(e))
	}
	if _, err := svc.RestoreFloor(as("lead-1", "member"), "sales", policy.KindPayments); err != nil {
		t.Fatalf("restoring the floor adds supervision: %v", err)
	}
}

func TestExceptionsNeedALevelFirstAndAreValidated(t *testing.T) {
	svc, h := permService(t)
	owner := as("owner-1", "owner")
	if _, err := svc.SetException(owner, "sales", policy.KindMessages, application.ExceptionInput{Mode: policy.ModeNever}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("an agent on the previous behaviour must choose a level first: %v", err)
	}
	if _, err := svc.ChooseLevel(owner, "sales", application.LevelImportant); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetException(owner, "sales", policy.KindMessages, application.ExceptionInput{Mode: policy.ModeAskIf}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ask_if without a condition: %v", err)
	}
	amt := 300.0
	v, err := svc.SetException(owner, "sales", policy.KindMessages, application.ExceptionInput{Mode: policy.ModeAskIf, If: &policy.AskIf{AmountOver: &amt}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Categories {
		if c.Category == policy.KindMessages && (c.Effective != policy.ModeAskIf || c.Exception == nil || c.Exception.CreatedBy != "owner-1") {
			t.Fatalf("messages: %+v", c)
		}
		if c.Category == policy.KindPayments && c.Effective != policy.ModeAsk {
			t.Fatalf("payments stay on the floor: %+v", c)
		}
	}
	// The engine applies it.
	ps := &application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()}
	ag, _ := h.store.GetAgent(context.Background(), domain.DemoOrgID, "sales")
	if d := ps.Decide(context.Background(), application.PolicyInput{Agent: ag, Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"amount": 50}}); !d.Allowed() {
		t.Fatalf("below the amount the email runs: %+v", d)
	}
	if d := ps.Decide(context.Background(), application.PolicyInput{Agent: ag, Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"amount": 500}}); d.RuleID != "perm.if_amount:messages" {
		t.Fatalf("above it asks and says which rule asked: %+v", d)
	}
	if _, err := svc.RemoveException(owner, "sales", policy.KindMessages); err != nil {
		t.Fatal(err)
	}
	if e := h.auditEntries("permissions.exception_removed"); len(e) != 1 {
		t.Fatalf("removal audited: %+v", e)
	}
}

func TestLevelOptionsRespectTheRoleCeiling(t *testing.T) {
	svc, _ := permService(t)
	v, err := svc.View(as("owner-1", "owner"), "sales") // sales: ceiling rules
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range v.Options {
		if o.Level == application.LevelAlone && (o.Allowed || o.Reason != "role_ceiling") {
			t.Fatalf("works alone is above the ceiling of sales: %+v", o)
		}
		if o.Level == application.LevelImportant && !o.Allowed {
			t.Fatalf("only what matters is allowed: %+v", o)
		}
	}
	if _, err := svc.ChooseLevel(as("owner-1", "owner"), "sales", application.LevelAlone); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("the server refuses a level above the ceiling: %v", err)
	}
}

func TestNewAgentsAdoptTheFactoryRulesAndTheDefaultLevel(t *testing.T) {
	svc, h := permService(t)
	if err := svc.AdoptNew(context.Background(), "sales"); err != nil {
		t.Fatal(err)
	}
	st, _ := h.store.GetOrgSettings(context.Background(), domain.DemoOrgID)
	if _, ok := st.Rules.Permissions.For("sales"); !ok {
		t.Fatal("a new agent gets the factory rules")
	}
	if _, err := svc.SetDefault(as("member-1", "member"), application.LevelAlone); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("members do not change the default: %v", err)
	}
	if _, err := svc.SetDefault(as("admin-1", "admin"), application.LevelAlone); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdoptNew(context.Background(), "legal"); err != nil { // legal: ceiling rules
		t.Fatal(err)
	}
	if a, _ := h.store.GetAgent(context.Background(), domain.DemoOrgID, "legal"); a.Autonomy != "rules" {
		t.Fatalf("the default is capped by the role's ceiling: %s", a.Autonomy)
	}
	ov, err := svc.Overview(as("admin-1", "admin"))
	if err != nil || ov.DefaultLevel != application.LevelAlone || !ov.CanEditDefault {
		t.Fatalf("overview: %+v %v", ov, err)
	}
}

func TestApproveAndDoNotAskAgain(t *testing.T) {
	svc, h := permService(t)
	svc.Approvals = h.store
	svc.ProjectScope = func(_ context.Context, _, projectID string) (string, bool) { return "b1", projectID == "p1" }
	owner := as("owner-1", "owner")
	if _, err := svc.ChooseLevel(owner, "sales", application.LevelImportant); err != nil {
		t.Fatal(err)
	}
	mk := func(id, rule, project string) {
		if err := h.store.CreateApproval(context.Background(), domain.DemoOrgID, domain.Approval{ID: id, AgentID: "sales", Action: "send",
			Risk: "low", Status: domain.ApprovalApproved, PolicyRule: rule, ProjectID: project}); err != nil {
			t.Fatal(err)
		}
	}
	mk("ap-floor", "perm.floor:payments", "")
	mk("ap-org", "org.amount", "")
	mk("ap-msg", "perm.important:messages", "p1")
	mk("ap-pub", "perm.important:publish", "")
	for _, id := range []string{"ap-floor", "ap-org"} {
		if _, err := svc.Remember(owner, id, application.RememberAgent); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("%s: the floor and organization rules cannot be remembered: %v", id, err)
		}
	}
	if _, err := svc.Remember(as("member-1", "member"), "ap-pub", application.RememberAgent); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("only who may edit the agent: %v", err)
	}
	r, err := svc.Remember(owner, "ap-pub", application.RememberAgent)
	if err != nil || r.Category != policy.KindPublish {
		t.Fatalf("agent scope: %+v %v", r, err)
	}
	v, _ := svc.View(owner, "sales")
	for _, c := range v.Categories {
		if c.Category == policy.KindPublish && (c.Exception == nil || c.Exception.Source != "approval:ap-pub" || c.Effective != policy.ModeNever) {
			t.Fatalf("the exception says where it came from: %+v", c)
		}
	}
	if _, err := svc.Remember(owner, "ap-pub", application.RememberBatch); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("no project behind the approval: %v", err)
	}
	r, err = svc.Remember(owner, "ap-msg", application.RememberBatch)
	if err != nil || r.ScopeGrant == nil || r.ScopeGrant.Kind != policy.ScopeBatch || r.ScopeGrant.ScopeID != "b1" {
		t.Fatalf("batch scope: %+v %v", r, err)
	}
	// The engine applies it only inside the batch.
	ps := &application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()}
	ps.SetScopeResolver(func(_ context.Context, _, requestID string) (string, string, bool) {
		if requestID == "req-in" {
			return "p2", "b1", true
		}
		return "p3", "", true
	})
	ag, _ := h.store.GetAgent(context.Background(), domain.DemoOrgID, "sales")
	in := application.PolicyInput{Agent: ag, Tool: "email", Action: "send", Risk: "low"}
	if d := ps.Decide(application.WithRequestID(context.Background(), "req-in"), in); !d.Allowed() {
		t.Fatalf("inside the batch the email runs: %+v", d)
	}
	if d := ps.Decide(application.WithRequestID(context.Background(), "req-out"), in); d.RuleID != "perm.important:messages" {
		t.Fatalf("outside the batch it still asks: %+v", d)
	}
	gs, _ := svc.Scopes(owner, "", "b1")
	if len(gs) != 1 {
		t.Fatalf("listed for the batch: %+v", gs)
	}
	if err := svc.RevokeScope(owner, gs[0].ID); err != nil {
		t.Fatal(err)
	}
	if d := ps.Decide(application.WithRequestID(context.Background(), "req-in"), in); d.Allowed() {
		t.Fatalf("revoked: it asks again: %+v", d)
	}
	if e := h.auditEntries("permissions.scope_revoked"); len(e) != 1 {
		t.Fatalf("revocation audited: %+v", e)
	}
}
