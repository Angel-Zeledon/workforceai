package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// fakeAdvocate is the runtime capability of the review: it counts calls and remembers the last request.
type fakeAdvocate struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  application.CounterargumentRequest
	err   error
	resp  application.CounterargumentResponse
}

func (f *fakeAdvocate) Counterargument(_ context.Context, in application.CounterargumentRequest) (application.CounterargumentResponse, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = in
	f.mu.Unlock()
	return f.resp, f.err
}

func okAdvocate() *fakeAdvocate {
	return &fakeAdvocate{resp: application.CounterargumentResponse{Counterargument: "  Mejor esperar.  ", Risks: []string{"importe", "", "destinatario"}, Checks: []string{"verifica"},
		Usage: &application.Usage{Model: "sim", InputTokens: 800, OutputTokens: 200, CostUSD: 0.004}, Model: "sim", Provider: "simulation"}}
}

// stubBudget replaces the orchestrator's guard and reservation to force the degraded paths.
type stubBudget struct {
	verdict application.GuardVerdict
	ex      *application.Exceeded
}

func (s stubBudget) AdmitReview(context.Context, string) application.GuardVerdict { return s.verdict }
func (s stubBudget) ReservePlanner(context.Context, domain.UsageKind, any) (*application.Reservation, *application.Exceeded) {
	return nil, s.ex
}
func (stubBudget) RecordPlannerUsage(context.Context, application.Usage, *application.Reservation) {}
func (stubBudget) NoteProviderNoBalance(context.Context, string, error) bool                       { return false }

func newAdvocate(h *harness, rt any) *application.DevilsAdvocate {
	return &application.DevilsAdvocate{Store: h.store, Core: h.store, Runtime: rt, Budget: h.orch,
		Rec: &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub}, Cfg: application.DefaultConfig()}
}

// pendingApproval stores a pending approval of the demo org.
func pendingApproval(t *testing.T, h *harness, risk string, required int) domain.Approval {
	t.Helper()
	ap, _, err := h.appr.Request(context.Background(), domain.Approval{AgentID: "sales", Action: "email.send", Title: "Enviar propuesta",
		Details: "Enviar correo con password=hunter2hunter2 a cliente@example.com", Risk: risk, RequiredApprovals: required})
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

func auditCount(t *testing.T, h *harness, action string) int {
	t.Helper()
	page, err := h.store.QueryAudit(context.Background(), domain.DemoOrgID, domain.AuditQuery{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

func TestIsHighRiskApprovalReadsTheStoredRiskAndPolicy(t *testing.T) {
	for _, c := range []struct {
		risk     string
		required int
		want     bool
	}{{"high", 1, true}, {"HIGH", 0, true}, {" high ", 1, true}, {"medium", 1, false}, {"low", 1, false}, {"", 1, false}, {"low", 2, true}} {
		if got := application.IsHighRiskApproval(domain.Approval{Risk: c.risk, RequiredApprovals: c.required}); got != c.want {
			t.Errorf("risk=%q required=%d: got %v", c.risk, c.required, got)
		}
	}
}

func TestCounterargumentIsGeneratedOnceCachedAndCharged(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := okAdvocate()
	da := newAdvocate(h, rt)
	ap := pendingApproval(t, h, "high", 1)
	ctx := application.WithActor(context.Background(), "ana")

	if _, err := da.Get(ctx, ap.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("before generating: %v", err)
	}
	c, err := da.Generate(ctx, ap.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if c.Counterargument != "Mejor esperar." || len(c.Risks) != 2 || !c.Advisory || c.Cached || c.CostUSD != 0.004 || c.GeneratedBy != "ana" {
		t.Fatalf("review = %+v", c)
	}
	if rt.last.Locale != "en" || rt.last.Approval.RequiredApprovals != 1 || rt.last.Approval.Risk != "high" {
		t.Fatalf("request = %+v", rt.last)
	}
	again, err := da.Generate(ctx, ap.ID, "")
	if err != nil || !again.Cached || again.Counterargument != c.Counterargument {
		t.Fatalf("second call = %+v %v", again, err)
	}
	if got, err := da.Get(ctx, ap.ID); err != nil || got.Counterargument != c.Counterargument {
		t.Fatalf("get = %+v %v", got, err)
	}
	if n := rt.calls.Load(); n != 1 {
		t.Fatalf("runtime called %d times, want 1 (cached)", n)
	}
	// Counted in the ledger once (the existing planner mechanism) and audited.
	usage, _ := h.store.ListUsage(ctx, domain.DemoOrgID)
	if len(usage) != 1 || usage[0].CostUSD <= 0 {
		t.Fatalf("ledger = %+v", usage)
	}
	if auditCount(t, h, "approval.counterargument") != 1 {
		t.Fatal("generation was not audited exactly once")
	}
}

func TestCounterargumentSendsOnlyTheRedactedCard(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := okAdvocate()
	ap := pendingApproval(t, h, "high", 1)
	if _, err := newAdvocate(h, rt).Generate(context.Background(), ap.ID, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rt.last.Approval.Details, "hunter2hunter2") || !strings.Contains(rt.last.Approval.Details, "REDACTED") {
		t.Fatalf("details were not redacted: %q", rt.last.Approval.Details)
	}
	if rt.last.Approval.AgentRole == "" {
		t.Fatal("the agent role should travel with the card")
	}
}

func TestCounterargumentConcurrentClicksCostOneCall(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := okAdvocate()
	da := newAdvocate(h, rt)
	ap := pendingApproval(t, h, "high", 1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := da.Generate(context.Background(), ap.ID, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := rt.calls.Load(); n != 1 {
		t.Fatalf("runtime called %d times, want 1", n)
	}
}

func TestCounterargumentOnlyForHighRiskPendingApprovals(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := okAdvocate()
	da := newAdvocate(h, rt)
	low := pendingApproval(t, h, "low", 1)
	if _, err := da.Generate(context.Background(), low.ID, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("low risk: %v", err)
	}
	dual := pendingApproval(t, h, "low", 2) // the policy engine asked for two approvals: treated as high risk
	if _, err := da.Generate(context.Background(), dual.ID, ""); err != nil {
		t.Fatalf("double approval: %v", err)
	}
	done := pendingApproval(t, h, "high", 1)
	if _, err := h.appr.Decide(application.WithActor(context.Background(), "ana"), done.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	if _, err := da.Generate(context.Background(), done.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("decided approval: %v", err)
	}
	if _, err := da.Generate(context.Background(), "nope", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown approval: %v", err)
	}
	if n := rt.calls.Load(); n != 1 {
		t.Fatalf("runtime called %d times, want 1 (only the double approval)", n)
	}
}

func TestCounterargumentIsOrganizationScoped(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	da := newAdvocate(h, okAdvocate())
	ap := pendingApproval(t, h, "high", 1)
	if _, err := da.Generate(context.Background(), ap.ID, ""); err != nil {
		t.Fatal(err)
	}
	other := application.WithOrg(context.Background(), "other-org")
	if _, err := da.Get(other, ap.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another organization reads the review: %v", err)
	}
	// (The memory store does not partition approvals by organization; the Postgres test covers Generate under RLS.)
}

func TestCounterargumentAgentsAndSystemCannotAsk(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := okAdvocate()
	da := newAdvocate(h, rt)
	ap := pendingApproval(t, h, "high", 1)
	for _, actor := range []string{"agent:sales", "system", "sales", "orchestrator"} {
		if _, err := da.Generate(application.WithActor(context.Background(), actor), ap.ID, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: %v", actor, err)
		}
	}
	if rt.calls.Load() != 0 {
		t.Fatal("a non-human principal reached the model")
	}
}

func TestCounterargumentDegradesWithoutTouchingTheApproval(t *testing.T) {
	cases := []struct {
		name string
		da   func(h *harness) *application.DevilsAdvocate
		code string
	}{
		{"runtime error", func(h *harness) *application.DevilsAdvocate {
			return newAdvocate(h, &fakeAdvocate{err: errors.New("model down")})
		}, application.CounterargumentUnavailable},
		{"empty answer", func(h *harness) *application.DevilsAdvocate { return newAdvocate(h, &fakeAdvocate{}) }, application.CounterargumentUnavailable},
		{"runtime without the capability", func(h *harness) *application.DevilsAdvocate { return newAdvocate(h, struct{}{}) }, application.CounterargumentUnavailable},
		{"kill switch or paused agent", func(h *harness) *application.DevilsAdvocate {
			da := newAdvocate(h, okAdvocate())
			da.Budget = stubBudget{verdict: application.GuardVerdict{Code: "kill_switch_active"}}
			return da
		}, application.CounterargumentBlocked},
		{"spending cap", func(h *harness) *application.DevilsAdvocate {
			da := newAdvocate(h, okAdvocate())
			da.Budget = stubBudget{verdict: application.GuardVerdict{Allowed: true}, ex: &application.Exceeded{Scope: domain.ScopeOrg}}
			return da
		}, application.CounterargumentOverBudget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, &fakeRuntime{}, nil)
			ap := pendingApproval(t, h, "high", 1)
			da := c.da(h)
			_, err := da.Generate(context.Background(), ap.ID, "")
			var ce *application.CounterargumentError
			if !errors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("err = %v, want code %s", err, c.code)
			}
			if _, err := da.Get(context.Background(), ap.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("a failed review must not be cached")
			}
			if auditCount(t, h, "approval.counterargument_failed") != 1 {
				t.Fatal("the failure was not audited")
			}
			// The approval is untouched and still decidable by a human.
			cur, _ := h.store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
			if cur.Status != domain.ApprovalPending {
				t.Fatalf("status = %s", cur.Status)
			}
			got, err := h.appr.Decide(application.WithActor(context.Background(), "ana"), ap.ID, "approve", "ok")
			if err != nil || got.Status != domain.ApprovalApproved {
				t.Fatalf("decide after a failed review: %+v %v", got, err)
			}
		})
	}
}

func TestCounterargumentIsAdvisoryAndNeverDecides(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	da := newAdvocate(h, okAdvocate())
	ap := pendingApproval(t, h, "high", 1)
	if _, err := da.Generate(application.WithActor(context.Background(), "ana"), ap.ID, ""); err != nil {
		t.Fatal(err)
	}
	cur, _ := h.store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
	if cur.Status != domain.ApprovalPending || len(cur.Decisions) != 0 {
		t.Fatalf("the review changed the approval: %+v", cur)
	}
	if auditCount(t, h, "approval.approved")+auditCount(t, h, "approval.rejected") != 0 {
		t.Fatal("the review produced a decision")
	}
}
