package application_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// ---- helpers ----

func toolRuntime(reqs ...application.ToolRequest) *fakeRuntime {
	return &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Objectives: []string{"x"}, Tasks: []application.PlannedTask{{Key: "a", Title: "Tarea", AgentID: "sales"}}}, nil
		},
		runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			res := okResult("hecho")
			res.ToolRequests = reqs
			return res, nil
		},
	}
}

// govHarness is a harness whose organization has the given approval rules and
// the policy service installed.
func govHarness(t *testing.T, rt application.Runtime, rules catalog.PackRules) *harness {
	t.Helper()
	h := newHarness(t, rt, nil)
	h.setRules(rules)
	h.orch.SetPolicy(&application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()})
	return h
}

func (h *harness) setRules(r catalog.PackRules) {
	st := application.DefaultOrgSettings()
	st.Rules = r
	if err := h.store.PutOrgSettings(context.Background(), domain.DemoOrgID, st); err != nil {
		h.t.Fatal(err)
	}
}

func as(user, role string) context.Context {
	return application.WithActorRole(application.WithActor(context.Background(), user), role)
}

func (h *harness) auditEntries(action string) []domain.AuditLog {
	var out []domain.AuditLog
	for _, a := range h.store.Audit() {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

func payment(amount any) application.ToolRequest {
	return application.ToolRequest{Tool: "ledger", Action: "make_payment", Risk: "low", Args: map[string]any{"amount": amount, "account": "ES00 1234"}}
}

func gov(g policy.Governance) catalog.PackRules {
	return catalog.PackRules{AlwaysApprove: []string{}, Governance: &g}
}

func (h *harness) waitApproval() domain.Approval {
	h.t.Helper()
	var ap domain.Approval
	ap = h.awaitApproval()
	return ap
}

func (h *harness) reload(id string) domain.Approval {
	h.t.Helper()
	ap, err := h.store.GetApproval(context.Background(), domain.DemoOrgID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return ap
}

// ---- baseline parity: no rules, nothing changes ----

func TestWithoutRulesTheBaselineDecidesExactlyAsBefore(t *testing.T) {
	cfg := application.DefaultConfig()
	h := newHarness(t, toolRuntime(), nil)
	svc := &application.PolicyService{Store: h.store, Cfg: cfg}
	for _, autonomy := range []string{"suggest", "approve_each", "rules", "autonomous", ""} {
		for _, risk := range []string{"low", "medium", "high", "HIGH", ""} {
			for _, action := range []string{"search", "send_proposal", "send_contract", "update_deal", "make_payment"} {
				want := strings.EqualFold(risk, "high") || cfg.ApprovalActions[action] || autonomy == "suggest" || autonomy == "approve_each"
				// An enormous amount and a stranger recipient must not matter without rules.
				v := svc.Decide(context.Background(), application.PolicyInput{
					Agent: domain.Agent{ID: "sales", Autonomy: autonomy}, Tool: "email", Action: action, Risk: risk,
					Args: map[string]any{"amount": 99999999, "to": "stranger@evil.test"}})
				if v.NeedsApproval() != want || v.Denied() {
					t.Fatalf("autonomy=%q risk=%q action=%q: %+v, want approval=%v", autonomy, risk, action, v, want)
				}
				if v.Source != "baseline" {
					t.Fatalf("no rules configured: the baseline decides, got %q", v.Source)
				}
			}
		}
	}
}

func TestDemoScenarioIsUnchangedWithThePolicyServiceInstalled(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), nil)
	h.orch.SetPolicy(&application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()})
	reqID, err := h.orch.Submit(context.Background(), "enviar propuesta de $50,000")
	if err != nil {
		t.Fatal(err)
	}
	ap := h.waitApproval()
	if ap.Action != "send_proposal" || ap.RequiredApprovals != 1 || ap.RequiredRole != "" || len(ap.Decisions) != 0 {
		t.Fatalf("the demo approval is a plain single approval: %+v", ap)
	}
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatalf("one approval is enough in the demo: %v", err)
	}
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatal("the request must complete")
	}
}

// ---- rules seeded by onboarding packs are applied ----

func TestRulesSeededByTheOnboardingPackAreApplied(t *testing.T) {
	rt := toolRuntime(payment(1500))
	h := newHarness(t, rt, nil)
	h.orch.SetPolicy(&application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()})
	oc := &application.OrgConfig{Cfg: application.DefaultConfig(), Store: h.store, Core: h.store, Orch: h.orch, Rec: &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: nil}}
	oc.Rec.Log = discardLog()
	if _, err := oc.Onboard(context.Background(), application.OnboardInput{PackKey: "general"}); err != nil {
		t.Fatal(err)
	}
	// The pack puts sales in approve_each; a mature agent in rules mode is what the amount rule is about.
	if err := h.store.SetAgentAutonomy(context.Background(), domain.DemoOrgID, "sales", "rules"); err != nil {
		t.Fatal(err)
	}
	// "general" seeds approval_amount_usd=1000: a payment of 1500 by an agent in
	// "rules" mode used to run unattended; now it waits for a human.
	if _, err := h.orch.Submit(context.Background(), "pagar"); err != nil {
		t.Fatal(err)
	}
	ap := h.waitApproval()
	if ap.Action != "make_payment" || ap.PolicyRule != "org.amount" {
		t.Fatalf("approval = %+v", ap)
	}
	dec := h.auditEntries("policy.decision")
	if len(dec) == 0 {
		t.Fatal("the decision must be audited")
	}
}

// ---- amount, role, new recipient, deny, limits ----

func TestApproverRoleByAmountAndAction(t *testing.T) {
	g := policy.Governance{AmountTiers: []policy.AmountTier{{AmountAbove: 5000, Role: "admin"}, {AmountAbove: 50000, Role: "owner"}}}
	h := govHarness(t, toolRuntime(payment(60000)), gov(g))
	if _, err := h.orch.Submit(as("ana", "member"), "pagar"); err != nil {
		t.Fatal(err)
	}
	ap := h.waitApproval()
	if ap.RequiredRole != "owner" || ap.RequiredApprovals != 1 || ap.RequestedBy != "ana" {
		t.Fatalf("approval = %+v", ap)
	}
	if _, err := h.appr.Decide(as("admin1", "admin"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an admin cannot approve an owner-level amount: %v", err)
	}
	if _, err := h.appr.Decide(as("viewer1", "viewer"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a viewer cannot approve: %v", err)
	}
	if h.reload(ap.ID).Status != domain.ApprovalPending {
		t.Fatal("refused decisions leave the approval pending")
	}
	if got := h.auditEntries("approval.decision_refused"); len(got) != 2 {
		t.Fatalf("refused attempts are audited, got %d", len(got))
	}
	if _, err := h.appr.Decide(as("boss", "owner"), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
}

func TestNewRecipientNeedsApprovalKnownDoesNot(t *testing.T) {
	g := policy.Governance{NewRecipient: true, KnownDomains: []string{"acme.com"}}
	known := application.ToolRequest{Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"to": "ana@acme.com", "subject": "hola"}}
	h := govHarness(t, toolRuntime(known), gov(g))
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone || len(h.auditEntries("tool.executed")) != 1 {
		t.Fatal("a known recipient needs no approval")
	}
	stranger := application.ToolRequest{Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"to": "ceo@rival.com"}}
	h2 := govHarness(t, toolRuntime(stranger), gov(g))
	if _, err := h2.orch.Submit(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if ap := h2.waitApproval(); ap.PolicyRule != "org.new_recipient" {
		t.Fatalf("approval = %+v", ap)
	}
}

func TestDeniedActionNeverRunsAndIsAudited(t *testing.T) {
	g := policy.Governance{DenyActions: []string{"crm.delete_*"}}
	rt := toolRuntime(application.ToolRequest{Tool: "crm", Action: "delete_contact", Risk: "low", Args: map[string]any{"id": "c1"}})
	h := govHarness(t, rt, gov(g))
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatal("a denied tool request does not fail the task")
	}
	if len(h.auditEntries("tool.executed")) != 0 {
		t.Fatal("a denied action must not run")
	}
	den := h.auditEntries("tool.denied")
	if len(den) != 1 || !strings.HasPrefix(fmt.Sprint(den[0].Details.(map[string]any)["reason"]), "policy:org.deny") {
		t.Fatalf("tool.denied = %+v", den)
	}
	if _, ok := h.pendingApproval(); ok {
		t.Fatal("a deny does not ask for approval")
	}
}

func TestWindowedLimitStopsTheThirdCall(t *testing.T) {
	g := policy.Governance{Limits: []policy.LimitRule{{ID: "mail.window", Tool: "email", Action: "send*", WindowSeconds: 3600, MaxCalls: 2}}}
	send := application.ToolRequest{Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"to": "a@acme.com"}}
	h := govHarness(t, toolRuntime(send, send, send), gov(g))
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatal("request must finish")
	}
	if n := len(h.auditEntries("tool.executed")); n != 2 {
		t.Fatalf("executed = %d, want 2", n)
	}
	den := h.auditEntries("tool.denied")
	if len(den) != 1 || den[0].Details.(map[string]any)["reason"] != "policy:limit:mail.window" {
		t.Fatalf("denied = %+v", den)
	}
}

// ---- double approval ----

func dualRules() catalog.PackRules {
	return gov(policy.Governance{
		DualApproval: &policy.DualApproval{Actions: []string{"make_payment"}},
		AmountTiers:  []policy.AmountTier{{AmountAbove: 1000, Role: "admin"}},
	})
}

func TestDoubleApprovalNeedsTwoDistinctHumansAndNeverTheRequester(t *testing.T) {
	h := govHarness(t, toolRuntime(payment(5000)), dualRules())
	reqID, _ := h.orch.Submit(as("ana", "member"), "pagar a proveedor")
	ap := h.waitApproval()
	if ap.RequiredApprovals != 2 || ap.RequiredRole != "admin" || ap.RequestedBy != "ana" || !ap.NoSelfApproval || !strings.HasPrefix(ap.PolicyRule, "dual_approval") {
		t.Fatalf("approval = %+v", ap)
	}
	// The requester cannot approve, whatever their role.
	if _, err := h.appr.Decide(as("ana", "owner"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("the requester must not approve: %v", err)
	}
	// First approval: recorded, still pending, the task keeps waiting.
	first, err := h.appr.Decide(as("bob", "admin"), ap.ID, "approve", "primera")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != domain.ApprovalPending || len(first.Decisions) != 1 || first.Decisions[0].By != "bob" {
		t.Fatalf("after the first approval: %+v", first)
	}
	if h.reload(ap.ID).Status != domain.ApprovalPending || h.request(reqID).Status != domain.RequestAwaitingApproval {
		t.Fatal("one approval must not release a double-approval action")
	}
	if h.pub.count(domain.EvApprovalProgress) != 1 || len(h.auditEntries("approval.partial")) != 1 {
		t.Fatal("the first approval is announced and audited")
	}
	// The same person cannot be both approvers.
	if _, err := h.appr.Decide(as("bob", "admin"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("the same human twice: %v", err)
	}
	// The requester still cannot be the second approver.
	if _, err := h.appr.Decide(as("ana", "owner"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("the requester as second approver: %v", err)
	}
	// A second approver below the required role does not count.
	if _, err := h.appr.Decide(as("carl", "member"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a second approver below the role: %v", err)
	}
	if h.reload(ap.ID).Status != domain.ApprovalPending {
		t.Fatal("still pending")
	}
	done, err := h.appr.Decide(as("dave", "owner"), ap.ID, "approve", "segunda")
	if err != nil || done.Status != domain.ApprovalApproved || len(done.Decisions) != 2 {
		t.Fatalf("second approval: %+v, %v", done, err)
	}
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatal("the request completes after the second approval")
	}
	ex := h.auditEntries("tool.executed")
	if len(ex) != 1 {
		t.Fatalf("executed %d times", len(ex))
	}
	d := ex[0].Details.(map[string]any)
	if fmt.Sprint(d["approvers"]) != "[bob dave]" {
		t.Fatalf("the execution records both approvers: %v", d["approvers"])
	}
	final := h.auditEntries("approval.approved")
	if len(final) != 1 || ex[0].RequestID != reqID || final[0].RequestID != reqID {
		t.Fatalf("audit entries are tagged with the request: %+v / %+v", final, ex[0])
	}
}

func TestOneRejectionIsFinalEvenAfterAnApproval(t *testing.T) {
	h := govHarness(t, toolRuntime(payment(5000)), dualRules())
	reqID, _ := h.orch.Submit(as("ana", "member"), "pagar")
	ap := h.waitApproval()
	if _, err := h.appr.Decide(as("bob", "admin"), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if r, err := h.appr.Decide(as("dave", "admin"), ap.ID, "reject", "no"); err != nil || r.Status != domain.ApprovalRejected {
		t.Fatalf("%+v %v", r, err)
	}
	h.orch.Wait()
	if len(h.auditEntries("tool.executed")) != 0 {
		t.Fatal("a rejected action must not run")
	}
	if ts := h.tasks(reqID); ts["Tarea"].Status != domain.TaskBlocked {
		t.Fatalf("task = %s", ts["Tarea"].Status)
	}
}

func TestDoubleApprovalIsAHardRequirementForHighRiskEvenInAutonomousMode(t *testing.T) {
	g := policy.Governance{DualApproval: &policy.DualApproval{HighRisk: true}}
	h := govHarness(t, toolRuntime(application.ToolRequest{Tool: "ledger", Action: "update_budget", Risk: "high", Args: map[string]any{"x": 1}}), gov(g))
	if err := h.store.SetAgentAutonomy(context.Background(), domain.DemoOrgID, "sales", "autonomous"); err != nil {
		t.Fatal(err)
	}
	h.orch.Submit(as("ana", "member"), "x")
	if ap := h.waitApproval(); ap.RequiredApprovals != 2 {
		t.Fatalf("%+v", ap)
	}
}

func TestDualApprovalCannotBeSatisfiedWithoutDistinctHumans(t *testing.T) {
	// Demo mode has a single anonymous actor: it cannot approve twice (fail closed).
	h := govHarness(t, toolRuntime(payment(5000)), dualRules())
	h.orch.Submit(context.Background(), "x")
	ap := h.waitApproval()
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("the same anonymous actor twice: %v", err)
	}
	if h.reload(ap.ID).Status != domain.ApprovalPending {
		t.Fatal("it stays pending until a second human approves or it expires")
	}
}

// ---- agents never approve ----

func TestAgentsAndSystemNeverApprove(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), nil)
	h.orch.Submit(context.Background(), "enviar propuesta")
	ap := h.waitApproval()
	for _, actor := range []string{"agent:sales", "agent:assistant", "system", "orchestrator", "sales", "Sales"} {
		ctx := application.WithActor(context.Background(), actor)
		if _, err := h.appr.Decide(ctx, ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("actor %q must be refused, got %v", actor, err)
		}
		if _, err := h.appr.Decide(ctx, ap.ID, "reject", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("actor %q must not reject either, got %v", actor, err)
		}
	}
	if h.reload(ap.ID).Status != domain.ApprovalPending {
		t.Fatal("still pending")
	}
	if _, err := h.appr.Decide(as("human", "admin"), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
}

func TestNoRoleHasTheApproveCapabilityForAgents(t *testing.T) {
	for _, a := range domain.SeedAgents() {
		for _, p := range a.Permissions {
			if strings.Contains(p, "approv") {
				t.Fatalf("agent %s holds %q", a.ID, p)
			}
		}
	}
}

// ---- rules change while an approval waits ----

func TestApprovedActionIsRevalidatedAtExecutionTime(t *testing.T) {
	h := govHarness(t, toolRuntime(payment(500)), catalog.PackRules{AlwaysApprove: []string{"make_payment"}})
	reqID, _ := h.orch.Submit(context.Background(), "x")
	ap := h.waitApproval()
	// An owner forbids the action while the approval is pending.
	h.setRules(catalog.PackRules{AlwaysApprove: []string{"make_payment"}, Governance: &policy.Governance{DenyActions: []string{"make_payment"}}})
	if _, err := h.appr.Decide(as("admin", "admin"), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if len(h.auditEntries("tool.executed")) != 0 {
		t.Fatal("an approved action that the rules now deny must not run")
	}
	if ts := h.tasks(reqID); ts["Tarea"].Status != domain.TaskBlocked {
		t.Fatalf("task = %s", ts["Tarea"].Status)
	}
}

// ---- audit content ----

func TestAuditHoldsMetadataNeverContentOrSecrets(t *testing.T) {
	const secret = "TOP-SECRET-BODY-4711"
	rt := toolRuntime(application.ToolRequest{Tool: "email", Action: "send_proposal", Risk: "medium",
		Args: map[string]any{"to": "ana@acme.com", "subject": secret, "body": secret, "api_key": "sk-live-abcdefghijklmnopqrstuvwxyz", "amount": 10}})
	h := govHarness(t, rt, catalog.PackRules{AlwaysApprove: []string{}, ApprovalAmountUSD: 5})
	reqID, _ := h.orch.Submit(as("ana", "member"), "mensaje con "+secret)
	ap := h.waitApproval()
	if _, err := h.appr.Decide(as("bob", "admin"), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	raw, _ := json.Marshal(h.store.Audit())
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "sk-live-abc") {
		t.Fatalf("content or a secret reached the audit trail:\n%s", raw)
	}
	dec := h.auditEntries("policy.decision")
	if len(dec) != 1 {
		t.Fatalf("policy.decision entries = %d", len(dec))
	}
	d := dec[0].Details.(map[string]any)
	for _, k := range []string{"tool", "action", "effect", "rule_id", "args_hash", "required_approvals", "autonomy"} {
		if _, ok := d[k]; !ok {
			t.Errorf("policy.decision misses %q: %v", k, d)
		}
	}
	if d["effect"] != "require_approval" || d["on_behalf_of"] != "ana" {
		t.Fatalf("policy.decision = %v", d)
	}
	if dec[0].RequestID != reqID {
		t.Fatalf("request id = %q, want %q", dec[0].RequestID, reqID)
	}
	ex := h.auditEntries("tool.executed")
	if len(ex) != 1 || fmt.Sprint(ex[0].Details.(map[string]any)["arg_keys"]) != "[amount api_key body subject to]" {
		t.Fatalf("tool.executed records which fields were sent: %+v", ex)
	}
}

// ---- audit service ----

func auditHarness(t *testing.T) (*application.AuditService, *harness) {
	t.Helper()
	h := newHarness(t, toolRuntime(), nil)
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}
	return &application.AuditService{Store: h.store, Rec: rec, Cfg: application.DefaultConfig()}, h
}

func seedAudit(t *testing.T, svc *application.AuditService, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		actor, action, req := "user-a", "approval.approved", "req-1"
		if i%3 == 1 {
			actor, action, req = "agent-x", "tool.executed", "req-2"
		}
		if i%3 == 2 {
			actor, action, req = "user-b", "policy.decision", "req-1"
		}
		svc.Rec.Audit(context.Background(), domain.AuditLog{Actor: actor, Action: action, Entity: "task", EntityID: fmt.Sprint("t", i), RequestID: req,
			Details: map[string]any{"i": i}})
		time.Sleep(time.Millisecond) // distinct, ordered timestamps
	}
}

func TestAuditQueryFiltersAndPagination(t *testing.T) {
	svc, _ := auditHarness(t)
	seedAudit(t, svc, 30)
	ctx := context.Background()
	q := func(q domain.AuditQuery) domain.AuditPage {
		t.Helper()
		p, err := svc.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := q(domain.AuditQuery{Actor: "user-b", Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("by actor: %d", len(p.Items))
	}
	if p := q(domain.AuditQuery{Action: "tool.executed", Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("by exact type: %d", len(p.Items))
	}
	if p := q(domain.AuditQuery{Action: "policy.*", Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("by type prefix: %d", len(p.Items))
	}
	if p := q(domain.AuditQuery{RequestID: "req-2", Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("by request: %d", len(p.Items))
	}
	if p := q(domain.AuditQuery{RequestID: "req-1", Actor: "user-b", Action: "policy.decision", Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("combined: %d", len(p.Items))
	}
	if p := q(domain.AuditQuery{Actor: "nobody", Limit: 1000}); len(p.Items) != 0 || p.NextCursor != "" {
		t.Fatalf("no match: %+v", p)
	}
	all := q(domain.AuditQuery{Limit: 1000})
	mid := all.Items[10].TS
	end := all.Items[20].TS
	if p := q(domain.AuditQuery{From: &mid, To: &end, Limit: 1000}); len(p.Items) != 10 {
		t.Fatalf("time range [from,to): %d", len(p.Items))
	}
	// Paging walks every entry exactly once, in both orders.
	for _, desc := range []bool{false, true} {
		seen, cursor := map[string]bool{}, ""
		var prev time.Time
		for pages := 0; ; pages++ {
			p := q(domain.AuditQuery{Limit: 7, Cursor: cursor, Desc: desc})
			for _, e := range p.Items {
				if seen[e.ID] {
					t.Fatalf("desc=%v: entry %s returned twice", desc, e.ID)
				}
				seen[e.ID] = true
				if !prev.IsZero() && ((!desc && e.TS.Before(prev)) || (desc && e.TS.After(prev))) {
					t.Fatalf("desc=%v: order broken", desc)
				}
				prev = e.TS
			}
			if p.NextCursor == "" {
				break
			}
			if pages > 10 {
				t.Fatal("paging does not terminate")
			}
			cursor = p.NextCursor
		}
		if len(seen) != 30 {
			t.Fatalf("desc=%v: paged %d of 30", desc, len(seen))
		}
	}
	if _, err := svc.Query(ctx, domain.AuditQuery{Cursor: "garbage"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestAuditExportJSONLAndCSV(t *testing.T) {
	svc, _ := auditHarness(t)
	seedAudit(t, svc, 12)
	ctx := context.Background()

	var js bytes.Buffer
	res, err := svc.Export(ctx, domain.AuditQuery{}, "jsonl", &js)
	if err != nil || res.Entries != 12 || res.HeadSeq != 12 {
		t.Fatalf("export: %+v %v", res, err)
	}
	recs, err := audit.ReadJSONL(js.Bytes())
	if err != nil || len(recs) != 12 {
		t.Fatalf("%d records, %v", len(recs), err)
	}
	if rep := audit.VerifyRecords(recs, true); !rep.OK {
		t.Fatalf("an unfiltered export verifies offline: %+v", rep)
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].Seq != recs[i-1].Seq+1 {
			t.Fatal("exports are oldest first")
		}
	}
	// The export is itself audited, but the entry about the export is not part of it.
	if p, _ := svc.Query(ctx, domain.AuditQuery{Action: "audit.exported"}); len(p.Items) != 1 {
		t.Fatalf("audit.exported entries = %d", len(p.Items))
	}
	for _, r := range recs {
		if r.Action == "audit.exported" {
			t.Fatal("the export must not contain the entry that records it")
		}
	}

	// Filtered CSV.
	var cs bytes.Buffer
	res, err = svc.Export(ctx, domain.AuditQuery{Actor: "user-a"}, "csv", &cs)
	if err != nil || res.Entries != 4 {
		t.Fatalf("csv export: %+v %v", res, err)
	}
	rows, err := csv.NewReader(&cs).ReadAll()
	if err != nil || len(rows) != 5 || rows[0][0] != "seq" {
		t.Fatalf("csv rows=%d err=%v", len(rows), err)
	}
	if _, err := svc.Export(ctx, domain.AuditQuery{}, "xml", &cs); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown format: %v", err)
	}
}

func TestAuditVerifyDetectsTamperingInTheStore(t *testing.T) {
	svc, h := auditHarness(t)
	seedAudit(t, svc, 8)
	ctx := context.Background()
	rep, err := svc.Verify(ctx, nil)
	if err != nil || !rep.OK || rep.Checked != 8 {
		t.Fatalf("honest trail: %+v %v", rep, err)
	}
	if len(h.auditEntries("audit.verified")) != 1 {
		t.Fatal("verification is audited")
	}

	h.store.UnsafeMutateAudit(func(e *[]domain.AuditLog) {
		for i := range *e {
			if (*e)[i].Seq == 4 {
				(*e)[i].Actor = "someone-else"
			}
		}
	})
	rep, err = svc.Verify(ctx, nil)
	if err != nil || rep.OK || rep.Reason != "hash_mismatch" || rep.BrokenAtSeq != 4 {
		t.Fatalf("modified entry: %+v %v", rep, err)
	}
	if len(h.auditEntries("audit.integrity_failed")) != 1 {
		t.Fatal("a failed verification is audited as audit.integrity_failed")
	}
}

func TestAuditVerifyDetectsDeletionsAndTruncation(t *testing.T) {
	svc, h := auditHarness(t)
	seedAudit(t, svc, 8)
	h.store.UnsafeMutateAudit(func(e *[]domain.AuditLog) { *e = append((*e)[:2], (*e)[3:]...) })
	rep, _ := svc.Verify(context.Background(), nil)
	if rep.OK || rep.Reason != "gap" {
		t.Fatalf("deleted middle entry: %+v", rep)
	}
	svc2, h2 := auditHarness(t)
	seedAudit(t, svc2, 8)
	h2.store.UnsafeMutateAudit(func(e *[]domain.AuditLog) { *e = (*e)[:5] })
	rep, _ = svc2.Verify(context.Background(), nil)
	if rep.OK || rep.Reason != "head_mismatch" {
		t.Fatalf("truncated tail: %+v", rep)
	}
}

func TestOrganizationsHaveIndependentChains(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	ctx := context.Background()
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}
	rec.Audit(application.WithOrg(ctx, "org-a"), domain.AuditLog{Actor: "u", Action: "x"})
	rec.Audit(application.WithOrg(ctx, "org-a"), domain.AuditLog{Actor: "u", Action: "y"})
	rec.Audit(application.WithOrg(ctx, "org-b"), domain.AuditLog{Actor: "u", Action: "z"})
	a, _ := h.store.AuditChain(ctx, "org-a", 0, 100)
	b, _ := h.store.AuditChain(ctx, "org-b", 0, 100)
	if len(a) != 2 || len(b) != 1 || b[0].Seq != 1 || b[0].PrevHash != "" {
		t.Fatalf("a=%d b=%+v", len(a), b)
	}
	for _, org := range []string{"org-a", "org-b"} {
		if rep, err := application.VerifyChain(ctx, h.store, org, nil); err != nil || !rep.OK {
			t.Fatalf("%s: %+v %v", org, rep, err)
		}
	}
	pa, _ := h.store.QueryAudit(ctx, "org-a", domain.AuditQuery{Limit: 100})
	if len(pa.Items) != 2 {
		t.Fatalf("a query only sees its own organization, got %d", len(pa.Items))
	}
}

// ---- rules as data ----

func TestUpdateRulesValidatesAuditsAndSurvivesOnboarding(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}
	oc := &application.OrgConfig{Cfg: application.DefaultConfig(), Store: h.store, Core: h.store, Orch: h.orch, Rec: rec}
	ctx := as("owner-1", "owner")
	amount := 2500.0
	always := []string{"Send Contract", "make_payment", "make_payment"}
	g := policy.Governance{DualApproval: &policy.DualApproval{Actions: []string{"make_payment"}}, KnownDomains: []string{" ACME.com "}}
	v, err := oc.UpdateRules(ctx, application.RulesUpdate{ApprovalAmountUSD: &amount, AlwaysApprove: &always, Governance: &g})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enforced || v.ApprovalAmountUSD != 2500 || fmt.Sprint(v.AlwaysApprove) != "[send_contract make_payment]" || v.Governance.KnownDomains[0] != "acme.com" {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Baseline) == 0 {
		t.Fatal("the view explains the invariants")
	}
	if e := h.auditEntries("policy.rules_updated"); len(e) != 1 || e[0].Actor != "owner-1" {
		t.Fatalf("rule changes are audited with the actor: %+v", e)
	}
	bad := []application.RulesUpdate{
		{Governance: &policy.Governance{AmountTiers: []policy.AmountTier{{AmountAbove: 1, Role: "member"}}}},
		{AlwaysApprove: &[]string{"bad name!"}},
		{ApprovalAmountUSD: func() *float64 { f := -1.0; return &f }()},
	}
	for i, b := range bad {
		if _, err := oc.UpdateRules(ctx, b); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("update %d must be rejected: %v", i, err)
		}
	}
	// Re-applying a pack keeps the governance an owner configured.
	if _, err := oc.Onboard(ctx, application.OnboardInput{PackKey: "general", Force: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := oc.Rules(ctx)
	if after.Governance.DualApproval == nil || after.ApprovalAmountUSD != 1000 {
		t.Fatalf("pack seeds the basics and keeps governance: %+v", after)
	}
	// Clearing governance with an empty object removes it.
	cleared, _ := oc.UpdateRules(ctx, application.RulesUpdate{Governance: &policy.Governance{}})
	if cleared.Governance.DualApproval != nil {
		t.Fatal("empty governance clears the rules")
	}
}

func TestPolicyFailsTowardsAHumanWhenRulesCannotBeRead(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	svc := &application.PolicyService{Store: brokenConfigStore{h.store}, Cfg: application.DefaultConfig()}
	v := svc.Decide(context.Background(), application.PolicyInput{Agent: domain.Agent{ID: "sales", Autonomy: "rules"}, Tool: "crm", Action: "update_deal", Risk: "low"})
	if !v.NeedsApproval() || v.RuleID != "policy.unavailable" {
		t.Fatalf("unreadable rules must not become allow: %+v", v)
	}
}

type brokenConfigStore struct{ application.ConfigStore }

func (brokenConfigStore) GetOrgSettings(context.Context, string) (application.OrgSettings, error) {
	return application.OrgSettings{}, errors.New("db down")
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- connection-backed actions go through the same rules ----

func legitSendRuntime() *spyRuntime {
	rt := &fakeRuntime{plan: onePlan("sales")}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send", Risk: "low",
			Args: map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto."}}}
		return res, nil
	}
	return &spyRuntime{fakeRuntime: rt}
}

func (w *wired) withRules(r catalog.PackRules) {
	w.setRules(r)
	w.orch.SetPolicy(&application.PolicyService{Store: w.store, Cfg: application.DefaultConfig()})
}

func (w *wired) approvePlan(reqID string) {
	w.waitFor("plan review", func() bool { _, err := w.gw.PlanGet(org, reqID); return err == nil })
	if _, err := w.gw.PlanDecide(context.Background(), org, reqID, "u1", true); err != nil {
		w.t.Fatal(err)
	}
}

func TestDoubleApprovalAppliesToConnectionBackedSends(t *testing.T) {
	w := wire(t, legitSendRuntime())
	write := w.conn("Gmail escritura", "mail.send")
	w.grant(write, "sales", "mail.send")
	w.withRules(gov(policy.Governance{DualApproval: &policy.DualApproval{Actions: []string{"email.send"}}}))

	reqID, _ := w.orch.Submit(as("ana", "member"), "envía la propuesta")
	w.approvePlan(reqID)
	ap := w.waitApproval()
	if ap.RequiredApprovals != 2 || ap.RequestedBy != "ana" || !ap.NoSelfApproval {
		t.Fatalf("the gateway approval carries the governance: %+v", ap)
	}
	if _, err := w.appr.Decide(as("ana", "owner"), ap.ID, "approve", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("the requester cannot approve a send: %v", err)
	}
	if _, err := w.appr.Decide(as("bob", "admin"), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if hs, _ := w.gw.Holds(context.Background(), org, "held"); len(hs) != 0 {
		t.Fatal("one approval must not release the send")
	}
	if _, err := w.appr.Decide(as("dave", "admin"), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if hs, _ := w.gw.Holds(context.Background(), org, "held"); len(hs) != 1 {
		t.Fatalf("after the second approval the send is held for the 60 s window: %+v", hs)
	}
	ex := w.auditEntries("tool.executed")
	if len(ex) == 0 || fmt.Sprint(ex[len(ex)-1].Details.(map[string]any)["approvers"]) != "[bob dave]" {
		t.Fatalf("both approvers are recorded: %+v", ex)
	}
}

func TestPolicyDenyStopsAConnectionBackedSendBeforeTheGateway(t *testing.T) {
	w := wire(t, legitSendRuntime())
	write := w.conn("Gmail escritura", "mail.send")
	w.grant(write, "sales", "mail.send")
	w.withRules(gov(policy.Governance{DenyActions: []string{"email.send"}}))
	reqID, _ := w.orch.Submit(context.Background(), "envía la propuesta")
	w.approvePlan(reqID)
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if _, ok := w.pendingApproval(); ok {
		t.Fatal("a denied send must not even ask for approval")
	}
	if hs, _ := w.gw.Holds(context.Background(), org, "held"); len(hs) != 0 || len(w.gm.FakeFor(write.ID).SentMessages()) != 0 {
		t.Fatal("nothing may reach the gateway")
	}
	if den := w.auditEntries("tool.denied"); len(den) != 1 {
		t.Fatalf("denied = %+v", den)
	}
}
