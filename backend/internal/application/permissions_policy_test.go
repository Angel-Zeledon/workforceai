package application_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// An agent without approval permissions decides exactly as before, even when another agent of the same
// organization has them (existing organizations do not change until someone touches the permissions).
func TestAgentsWithoutPermissionsDecideExactlyAsBefore(t *testing.T) {
	cfg := application.DefaultConfig()
	h := newHarness(t, toolRuntime(), nil)
	h.setRules(catalog.PackRules{AlwaysApprove: []string{}, Permissions: &policy.Permissions{
		Agents: map[string]policy.AgentPermissions{"legal": {}}}})
	svc := &application.PolicyService{Store: h.store, Cfg: cfg}
	for _, autonomy := range []string{"suggest", "approve_each", "rules", "autonomous", ""} {
		for _, risk := range []string{"low", "medium", "high", ""} {
			for _, action := range []string{"search", "send", "send_proposal", "update_deal", "make_payment", "delete"} {
				want := strings.EqualFold(risk, "high") || cfg.ApprovalActions[action] || autonomy == "suggest" || autonomy == "approve_each"
				v := svc.Decide(context.Background(), application.PolicyInput{
					Agent: domain.Agent{ID: "sales", Autonomy: autonomy}, Tool: "email", Action: action, Risk: risk,
					Args: map[string]any{"amount": 99999999, "to": "stranger@evil.test"}})
				if v.NeedsApproval() != want || v.Denied() || v.Source != "baseline" {
					t.Fatalf("autonomy=%q risk=%q action=%q: %+v, want approval=%v from the baseline", autonomy, risk, action, v, want)
				}
			}
		}
	}
}

func permsFor(agent string, a policy.AgentPermissions) catalog.PackRules {
	return catalog.PackRules{AlwaysApprove: []string{"send_proposal", "send_contract"},
		Permissions: &policy.Permissions{Agents: map[string]policy.AgentPermissions{agent: a}}}
}

func TestOnlyWhatMattersAsksForOutboundAndRunsInternalWork(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	h.setRules(permsFor("sales", policy.AgentPermissions{}))
	svc := &application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()}
	ag := domain.Agent{ID: "sales", Autonomy: "rules"}
	cases := []struct {
		tool, action, want, rule string
	}{
		{"email", "send", "require_approval", "perm.important:messages"},
		{"documents", "create", "allow", ""},
		{"documents", "read", "allow", ""},
		{"fs", "delete", "require_approval", "perm.floor:delete"},
		{"calendar", "schedule", "require_approval", "perm.important:other"},
	}
	for _, c := range cases {
		v := svc.Decide(context.Background(), application.PolicyInput{Agent: ag, Tool: c.tool, Action: c.action, Risk: "low"})
		if v.Effect != c.want || (c.rule != "" && v.RuleID != c.rule) {
			t.Errorf("%s.%s: %s (%s), want %s (%s)", c.tool, c.action, v.Effect, v.RuleID, c.want, c.rule)
		}
	}
}

func TestWorksAloneKeepsTheSafetyFloor(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	h.setRules(permsFor("analyst", policy.AgentPermissions{}))
	svc := &application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()}
	ag := domain.Agent{ID: "analyst", Autonomy: "autonomous"}
	for action, want := range map[string]string{"send": "allow", "make_payment": "require_approval", "publish_post": "allow"} {
		if v := svc.Decide(context.Background(), application.PolicyInput{Agent: ag, Tool: "x", Action: action, Risk: "low"}); v.Effect != want {
			t.Errorf("%s: %s (%s), want %s", action, v.Effect, v.RuleID, want)
		}
	}
}

func TestLiftedFloorSkipsTheAlwaysApproveListButNotHighRiskOrAmount(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	r := permsFor("analyst", policy.AgentPermissions{FloorLifts: []policy.FloorLift{{Category: policy.KindContracts, By: "owner"}}})
	r.ApprovalAmountUSD = 1000
	h.setRules(r)
	svc := &application.PolicyService{Store: h.store, Cfg: application.DefaultConfig()}
	ag := domain.Agent{ID: "analyst", Autonomy: "autonomous"}
	in := application.PolicyInput{Agent: ag, Tool: "documents", Action: "send_proposal", Risk: "medium"}
	if v := svc.Decide(context.Background(), in); !v.Allowed() {
		t.Fatalf("lifted floor, medium risk: %+v", v)
	}
	in.Risk = "high"
	if v := svc.Decide(context.Background(), in); !v.NeedsApproval() || v.RuleID != "baseline.risk_high" {
		t.Fatalf("high risk still asks: %+v", v)
	}
	in.Risk, in.Args = "low", map[string]any{"amount": 5000}
	if v := svc.Decide(context.Background(), in); !v.NeedsApproval() || v.RuleID != "org.amount" {
		t.Fatalf("the organization's amount threshold still asks: %+v", v)
	}
	// Another agent of the same organization keeps the floor.
	other := application.PolicyInput{Agent: domain.Agent{ID: "sales", Autonomy: "autonomous"}, Tool: "documents", Action: "send_proposal", Risk: "medium"}
	if v := svc.Decide(context.Background(), other); !v.NeedsApproval() {
		t.Fatalf("the lift is per agent: %+v", v)
	}
}

func TestPermissionsSurviveReapplyingTheOnboardingPack(t *testing.T) {
	h := newHarness(t, toolRuntime(), nil)
	h.setRules(permsFor("sales", policy.AgentPermissions{Exceptions: []policy.Exception{{Category: policy.KindPublish, Mode: policy.ModeAsk}}}))
	oc := &application.OrgConfig{Cfg: application.DefaultConfig(), Store: h.store, Core: h.store, Orch: h.orch, Rec: &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}}
	if _, err := oc.Onboard(context.Background(), application.OnboardInput{PackKey: "general", Force: true}); err != nil {
		t.Fatal(err)
	}
	st, _ := h.store.GetOrgSettings(context.Background(), domain.DemoOrgID)
	if a, ok := st.Rules.Permissions.For("sales"); !ok || len(a.Exceptions) != 1 {
		t.Fatalf("permissions lost after onboarding: %+v", st.Rules.Permissions)
	}
}
