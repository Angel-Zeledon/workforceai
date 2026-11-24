package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Proposing temporary helpers: the split call, the stored proposal that holds the request, accept / decline, the
// restart with an open proposal, the flag and 50 parts. Everything runs on a simulated runtime.

type splitRuntime struct {
	*helperRuntime
	smu   sync.Mutex
	n     int
	split func(application.SplitRequest) (application.SplitResponse, error)
}

func (r *splitRuntime) Split(_ context.Context, in application.SplitRequest) (application.SplitResponse, error) {
	r.smu.Lock()
	r.n++
	f := r.split
	r.smu.Unlock()
	return f(in)
}

func (r *splitRuntime) splitCalls() int {
	r.smu.Lock()
	defer r.smu.Unlock()
	return r.n
}

func splitInto(n int) func(application.SplitRequest) (application.SplitResponse, error) {
	return func(application.SplitRequest) (application.SplitResponse, error) {
		res := application.SplitResponse{WorthIt: true, Reason: "independientes", Source: "rules", Usage: &application.Usage{Model: "sim", CostUSD: 0.003}}
		for i := 1; i <= n; i++ {
			res.Parts = append(res.Parts, application.SplitPart{Label: fmt.Sprintf("Contratos %d", i), Instructions: fmt.Sprintf("contrato %d: texto", i)})
		}
		return res, nil
	}
}

func newSplitRuntime(n int) *splitRuntime {
	return &splitRuntime{helperRuntime: newHelperRuntime(), split: splitInto(n)}
}

func proposalCfg(c *application.Config) {
	helperCfg(c)
	c.ApprovalTimeout = time.Minute
}

func proposalTier(parts int) func(context.Context, string) policy.Tier {
	return func(context.Context, string) policy.Tier {
		return policy.Tier{ID: "max", HelpersParallel: 8, HelperParts: parts}
	}
}

func proposalHarness(t *testing.T, rt *splitRuntime, parts int, mutate func(*application.Config)) *harness {
	t.Helper()
	h := newHarness(t, rt, func(c *application.Config) {
		proposalCfg(c)
		if mutate != nil {
			mutate(c)
		}
	})
	h.orch.SetHelperLimits(proposalTier(parts))
	return h
}

func (h *harness) waitProposal(reqID string) domain.HelperGroup {
	h.t.Helper()
	var g domain.HelperGroup
	h.waitFor("a proposal", func() bool {
		cur, err := h.store.GetHelperGroupByRequest(context.Background(), domain.DemoOrgID, reqID)
		g = cur
		return err == nil && cur.Status == domain.HelperProposed && h.request(reqID).Status == domain.RequestAwaitingConfirmation
	})
	return g
}

func TestProposalHoldsTheRequestUntilThePersonAccepts(t *testing.T) {
	rt := newSplitRuntime(6)
	h := proposalHarness(t, rt, 20, nil)
	reqID, err := h.orch.Submit(context.Background(), "revisa estos contratos")
	if err != nil {
		t.Fatal(err)
	}
	g := h.waitProposal(reqID)
	if g.Count != 6 || g.Mode != domain.HelperModePosition || g.OwnerAgentID != helperOwner || g.MaxCount != 20 || g.Estimate.MaxUSD <= 0 || g.CapUSD != g.Estimate.MaxUSD || g.SpentUSD != 0 {
		t.Fatalf("proposal %+v", g)
	}
	time.Sleep(60 * time.Millisecond)
	if rt.callsOf("Responder") != 0 || rt.callsOf("Ayudante") != 0 {
		t.Fatal("nothing runs while the person has not decided")
	}
	if h.pub.count(domain.EvHelpersProposed) == 0 {
		t.Fatal("helpers.proposed must be published")
	}
	var split float64 // the split call is real spend, in the ledger
	us, _ := h.store.ListUsage(context.Background(), domain.DemoOrgID)
	for _, u := range us {
		split += u.CostUSD
	}
	if split < 0.003-1e-9 {
		t.Fatalf("the split call must be in the ledger: %v", split)
	}
	if h.auditCount("helpers.proposed") != 1 {
		t.Fatal("the proposal is audited")
	}

	got, err := h.orch.AcceptHelpers(context.Background(), reqID, 6, g.CapUSD+1, "alice")
	if err != nil || got.Status == domain.HelperProposed || got.AcceptedBy != "alice" {
		t.Fatalf("accept: %+v %v", got, err)
	}
	h.waitFor("the request to finish", func() bool { return h.request(reqID).Status == domain.RequestDone })
	if rt.callsOf("Ayudante") != 6 || rt.callsOf("Juntar") != 1 {
		t.Fatalf("parts=%d join=%d", rt.callsOf("Ayudante"), rt.callsOf("Juntar"))
	}
	if rt.callsOf("Responder correos") != 0 {
		t.Fatal("the plan's own task is replaced by the parts and the join")
	}
	again, err := h.orch.AcceptHelpers(context.Background(), reqID, 6, g.CapUSD+1, "alice") // double click
	if err != nil || again.ID != got.ID {
		t.Fatalf("accept twice must return the same group: %+v %v", again, err)
	}
	if rt.callsOf("Ayudante") != 6 {
		t.Fatal("a second accept creates and runs nothing")
	}
	h.waitFor("helpers.updated", func() bool { return h.pub.count(domain.EvHelpersUpdated) > 0 })
	if n := h.pub.count(domain.EvHelpersUpdated); n > 40 {
		t.Fatalf("updates are grouped, not one per token or call: %d", n)
	}
}

func TestDecliningLeavesTheNormalPlan(t *testing.T) {
	rt := newSplitRuntime(5)
	h := proposalHarness(t, rt, 20, nil)
	reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
	h.waitProposal(reqID)
	g, err := h.orch.DeclineHelpers(context.Background(), reqID, "alice")
	if err != nil || g.Status != domain.HelperDeclined {
		t.Fatalf("decline: %+v %v", g, err)
	}
	h.waitFor("the request to finish", func() bool { return h.request(reqID).Status == domain.RequestDone })
	if rt.callsOf("Responder correos") != 1 || rt.callsOf("Ayudante") != 0 {
		t.Fatalf("the normal plan runs: base=%d parts=%d", rt.callsOf("Responder correos"), rt.callsOf("Ayudante"))
	}
	if _, err := h.orch.DeclineHelpers(context.Background(), reqID, "alice"); err != nil {
		t.Fatalf("declining twice is idempotent: %v", err)
	}
	if _, err := h.orch.AcceptHelpers(context.Background(), reqID, 5, 5, "alice"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a declined proposal cannot be accepted: %v", err)
	}
}

func TestAcceptValidatesCountAndCap(t *testing.T) {
	rt := newSplitRuntime(6)
	h := proposalHarness(t, rt, 6, nil)
	reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
	g := h.waitProposal(reqID)
	ctx := context.Background()
	for name, c := range map[string]struct {
		count int
		cap   float64
	}{
		"count below 2":        {1, g.CapUSD + 1},
		"count above the max":  {7, g.CapUSD + 1},
		"cap below the min":    {6, g.Estimate.MinUSD / 2},
		"cap above the budget": {6, 1e6},
		"no cap":               {6, 0},
	} {
		if _, err := h.orch.AcceptHelpers(ctx, reqID, c.count, c.cap, "alice"); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if cur, _ := h.store.GetHelperGroupByRequest(ctx, domain.DemoOrgID, reqID); cur.Status != domain.HelperProposed {
		t.Fatal("a refused accept leaves the proposal open")
	}
	if got, err := h.orch.AcceptHelpers(ctx, reqID, 3, g.CapUSD+1, "alice"); err != nil || got.Count != 3 {
		t.Fatalf("3 helpers merge the 6 parts: %+v %v", got, err)
	}
	h.waitFor("the request to finish", func() bool { return h.request(reqID).Status == domain.RequestDone })
	if rt.callsOf("Ayudante") != 3 {
		t.Fatalf("3 helpers: %d", rt.callsOf("Ayudante"))
	}
}

func TestPlanLimitRefusesTooManyParts(t *testing.T) {
	rt := newSplitRuntime(6)
	h := proposalHarness(t, rt, 6, nil)
	reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
	g := h.waitProposal(reqID)
	h.orch.SetHelperLimits(proposalTier(5)) // the plan shrank after the offer
	_, err := h.orch.AcceptHelpers(context.Background(), reqID, 6, g.CapUSD+1, "alice")
	var pl *domain.PlanLimitError
	if !errors.As(err, &pl) || pl.Kind != "helper_parts" {
		// count above max_count is refused first (400); either is a refusal, the plan error is for stale offers
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("a stale offer is refused: %v", err)
		}
	}
}

func TestNoProposalWhenTheSplitIsNotWorthIt(t *testing.T) {
	cases := map[string]func(application.SplitRequest) (application.SplitResponse, error){
		"runtime error": func(application.SplitRequest) (application.SplitResponse, error) {
			return application.SplitResponse{}, errors.New("boom")
		},
		"not worth it": func(application.SplitRequest) (application.SplitResponse, error) {
			return application.SplitResponse{WorthIt: false, Reason: "dependen entre sí", Source: "rules"}, nil
		},
		"three parts": splitInto(3),
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			rt := newSplitRuntime(0)
			rt.split = f
			h := proposalHarness(t, rt, 20, nil)
			reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
			h.orch.Wait()
			if h.request(reqID).Status != domain.RequestDone || rt.callsOf("Responder correos") != 1 {
				t.Fatalf("the normal plan runs: %s", h.request(reqID).Status)
			}
			if _, err := h.store.GetHelperGroupByRequest(context.Background(), domain.DemoOrgID, reqID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("no group without a proposal")
			}
			if rt.splitCalls() != 1 {
				t.Fatalf("one split call: %d", rt.splitCalls())
			}
		})
	}
}

func TestNoSplitCallWithTheFlagOffOrWithoutTheCapability(t *testing.T) {
	rt := newSplitRuntime(6)
	h := newHarness(t, rt, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 }) // flag off
	reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
	h.orch.Wait()
	if rt.splitCalls() != 0 || h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("flag off: split calls=%d status=%s", rt.splitCalls(), h.request(reqID).Status)
	}
	if _, err := h.orch.AcceptHelpers(context.Background(), reqID, 4, 1, "alice"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accept with the flag off: %v", err)
	}
	if _, err := h.orch.DeclineHelpers(context.Background(), reqID, "alice"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("decline with the flag off: %v", err)
	}
	plain := proposalHarness(t, &splitRuntime{helperRuntime: newHelperRuntime(), split: splitInto(6)}, 20, nil)
	_ = plain
	h2 := newHarness(t, newHelperRuntime(), proposalCfg) // flag on, runtime without /v1/split
	id2, _ := h2.orch.Submit(context.Background(), "revisa estos contratos")
	h2.orch.Wait()
	if h2.request(id2).Status != domain.RequestDone {
		t.Fatal("a runtime without split just runs the plan")
	}
}

func TestSplitCallRespectsTheBudget(t *testing.T) {
	rt := newSplitRuntime(6)
	h := proposalHarness(t, rt, 20, func(c *application.Config) { c.BudgetUSD = 0.0000001 })
	reqID, _ := h.orch.Submit(context.Background(), "revisa estos contratos")
	h.orch.Wait()
	if rt.splitCalls() != 0 {
		t.Fatal("the split call is under the budget caps: with no money it is not made")
	}
	if _, err := h.store.GetHelperGroupByRequest(context.Background(), domain.DemoOrgID, reqID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("no proposal without money")
	}
}

func TestOpenProposalSurvivesARestart(t *testing.T) {
	w := newWorld(t)
	rt1 := newSplitRuntime(6)
	p1 := startProcess(t, w.store, w.pub, rt1, proposalCfg)
	p1.orch.SetHelperLimits(proposalTier(20))
	reqID, err := p1.orch.Submit(context.Background(), "revisa estos contratos")
	if err != nil {
		t.Fatal(err)
	}
	g := p1.waitProposal(reqID)
	p1.stop()
	meta, err := w.store.GetRunMeta(context.Background(), domain.DemoOrgID, reqID)
	if err != nil || meta.Gate == nil || meta.Gate.Kind != application.GateHelperProposal {
		t.Fatalf("the proposal gate must be in the run meta: %+v %v", meta, err)
	}

	rt2 := newSplitRuntime(6)
	p2 := startProcess(t, w.store, w.pub, rt2, proposalCfg)
	p2.orch.SetHelperLimits(proposalTier(20))
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	time.Sleep(100 * time.Millisecond)
	cur, _ := w.store.GetHelperGroupByRequest(context.Background(), domain.DemoOrgID, reqID)
	if cur.ID != g.ID || cur.Status != domain.HelperProposed || p2.request(reqID).Status != domain.RequestAwaitingConfirmation {
		t.Fatalf("the open proposal is still open and the request still waits: %+v / %s", cur, p2.request(reqID).Status)
	}
	if rt2.splitCalls() != 0 || rt2.callsOf("Responder") != 0 {
		t.Fatal("nothing is asked or run again after the restart")
	}
	if _, err := p2.orch.AcceptHelpers(context.Background(), reqID, 6, g.CapUSD+1, "alice"); err != nil {
		t.Fatal(err)
	}
	p2.waitFor("the request to finish", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	if rt2.callsOf("Ayudante") != 6 || rt2.callsOf("Responder correos") != 0 {
		t.Fatalf("parts=%d base=%d", rt2.callsOf("Ayudante"), rt2.callsOf("Responder correos"))
	}
}

func TestAcceptedBeforeARestartIsNotAskedAgain(t *testing.T) {
	w := newWorld(t)
	p1 := startProcess(t, w.store, w.pub, newSplitRuntime(6), proposalCfg)
	p1.orch.SetHelperLimits(proposalTier(20))
	reqID, _ := p1.orch.Submit(context.Background(), "revisa estos contratos")
	g := p1.waitProposal(reqID)
	// the person accepts, and the server dies before the request notices
	p1.stop()
	if _, err := p1.orch.AcceptHelpers(context.Background(), reqID, 6, g.CapUSD+1, "alice"); err != nil {
		t.Fatal(err)
	}
	rt2 := newSplitRuntime(6)
	p2 := startProcess(t, w.store, w.pub, rt2, proposalCfg)
	p2.orch.SetHelperLimits(proposalTier(20))
	if n, _ := p2.orch.Recover(context.Background(), nil); n != 1 {
		t.Fatalf("recover = %d", n)
	}
	p2.waitFor("the request to finish", func() bool { return p2.request(reqID).Status == domain.RequestDone })
	if rt2.callsOf("Ayudante") != 6 || rt2.callsOf("Responder correos") != 0 || rt2.splitCalls() != 0 {
		t.Fatalf("parts=%d base=%d split=%d", rt2.callsOf("Ayudante"), rt2.callsOf("Responder correos"), rt2.splitCalls())
	}
}

func TestFiftyPartsAreJoinedInBatches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		summary  int // characters of each part's result
		wantMids int // batch joins that call the model
	}{{"small results pass through", 40, 0}, {"big results are condensed", 6000, 5}} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newSplitRuntime(50)
			rt.part = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
				res := okResult(strings.Repeat("x", tc.summary))
				res.Usage = application.Usage{Model: "sim", CostUSD: 0.001}
				return res, nil
			}
			h := proposalHarness(t, rt, 50, func(c *application.Config) { c.MaxParallelPerOrg = 8 })
			reqID, _ := h.orch.Submit(context.Background(), "revisa estos 50 contratos")
			g := h.waitProposal(reqID)
			if g.Count != 50 || g.MaxCount != 50 {
				t.Fatalf("%+v", g)
			}
			if _, err := h.orch.AcceptHelpers(context.Background(), reqID, 50, 40, "alice"); err != nil {
				t.Fatal(err)
			}
			h.waitFor("the request to finish", func() bool { return h.request(reqID).Status == domain.RequestDone })
			parts, mids, finals := 0, 0, 0
			ts, _ := h.store.ListTasksByRequest(context.Background(), domain.DemoOrgID, reqID)
			var final domain.Task
			for _, t := range ts {
				switch {
				case t.Helper == nil:
				case t.Helper.Index > 0:
					parts++
				case strings.HasPrefix(t.Helper.Label, "unión parcial"):
					mids++
				default:
					finals++
					final = t
				}
			}
			if parts != 50 || mids != 5 || finals != 1 || len(final.DependsOn) != 5 {
				t.Fatalf("parts=%d mids=%d finals=%d final deps=%d", parts, mids, finals, len(final.DependsOn))
			}
			if got := rt.callsOf("Juntar un grupo"); got != tc.wantMids {
				t.Fatalf("batch joins that call the model = %d, want %d", got, tc.wantMids)
			}
			if rt.callsOf("Juntar los resultados") != 1 || rt.callsOf("Ayudante") != 50 {
				t.Fatalf("final=%d parts=%d", rt.callsOf("Juntar los resultados"), rt.callsOf("Ayudante"))
			}
			in, _ := rt.input("Juntar los resultados")
			if len(in.Context.DependencyOutputs) != 5 {
				t.Fatalf("the final join reads the 5 batches: %d", len(in.Context.DependencyOutputs))
			}
			got, _ := h.orch.HelperProgress(context.Background(), reqID)
			if got.Status != domain.HelperDone {
				t.Fatalf("group %s", got.Status)
			}
		})
	}
}

func TestHelpersWithoutAPositionAreCoordinatedByTheAssistant(t *testing.T) {
	rt := newSplitRuntime(5)
	h := proposalHarness(t, rt, 20, func(c *application.Config) { c.ChatStagger = 0 })
	ctx := application.WithHelpersFree(context.Background())
	if _, err := h.orch.PostChat(ctx, domain.ChatAgentPrefix+"analyst", "investiga estas 12 ciudades"); err == nil {
		t.Fatal("helpers without a position are only asked in the office conversation")
	}
	if _, err := h.orch.PostChat(ctx, domain.ChatOffice, "investiga estas 12 ciudades"); err != nil {
		t.Fatal(err)
	}
	var g domain.HelperGroup
	h.waitFor("a free proposal", func() bool {
		gs, _ := h.store.ListHelperGroups(context.Background(), domain.DemoOrgID, domain.HelperProposed)
		if len(gs) == 1 {
			g = gs[0]
		}
		return len(gs) == 1
	})
	if g.Mode != domain.HelperModeFree || g.OwnerAgentID != "assistant" {
		t.Fatalf("the assistant coordinates a free group: %+v", g)
	}
	if _, err := h.orch.DeclineHelpers(context.Background(), g.RequestID, "alice"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
}

func TestFreeHelpersThatCannotSplitSayItAndContinue(t *testing.T) {
	rt := newSplitRuntime(0)
	rt.split = func(application.SplitRequest) (application.SplitResponse, error) {
		return application.SplitResponse{WorthIt: false, Reason: "no hay partes independientes", Source: "rules"}, nil
	}
	h := proposalHarness(t, rt, 20, func(c *application.Config) { c.ChatStagger = 0 })
	ctx := application.WithHelpersFree(context.Background())
	if _, err := h.orch.PostChat(ctx, domain.ChatOffice, "investiga esto"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	said := false
	for _, m := range h.messages(domain.ChatOffice) {
		if m.From == "assistant" && strings.Contains(m.Text, "No pude repartirlo entre ayudantes") {
			said = true
		}
	}
	if !said {
		t.Fatal("the coordinator tells the person it could not be split")
	}
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(reqs) != 1 || reqs[0].Status != domain.RequestDone {
		t.Fatalf("it goes on as a normal request: %+v", reqs)
	}
}

func TestHelpersFreeIsRefusedWithTheFlagOff(t *testing.T) {
	h := newHarness(t, newSplitRuntime(5), func(c *application.Config) { c.ChatStagger = 0 })
	if _, err := h.orch.PostChat(application.WithHelpersFree(context.Background()), domain.ChatOffice, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("with the flag off: %v", err)
	}
}
