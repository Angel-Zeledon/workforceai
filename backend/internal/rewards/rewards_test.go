package rewards_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/rewards"
)

type capture struct {
	mu sync.Mutex
	ev []domain.Event
}

func (c *capture) Publish(_ context.Context, e domain.Event) error {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
	return nil
}

func (c *capture) types() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for _, e := range c.ev {
		out = append(out, e.Type)
	}
	return out
}

func (c *capture) count(typ string) (n int) {
	for _, t := range c.types() {
		if t == typ {
			n++
		}
	}
	return
}

type fixture struct {
	svc *rewards.Service
	cap *capture
	mem *memory.Store
	ctx context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(10), nil); err != nil {
		t.Fatal(err)
	}
	cp := &capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: mem, Pub: cp, Log: log}
	cat, err := rewards.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	svc := &rewards.Service{Store: rewards.NewMemStore(), Catalog: cat, Rules: rewards.DefaultRules(), Rec: rec, Log: log}
	return &fixture{svc: svc, cap: cp, mem: mem, ctx: application.WithOrg(context.Background(), "org-a")}
}

func done(id, agent string, review *domain.Review) domain.Task {
	out := &domain.StructuredOutput{Summary: "ok", Review: review}
	return domain.Task{ID: id, RequestID: "req-" + id, Title: "Task " + id, AgentID: agent, Status: domain.TaskDone, Output: out}
}

func TestCatalogIsValid(t *testing.T) {
	c, err := rewards.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Items) < 40 {
		t.Fatalf("catalog has %d items", len(c.Items))
	}
	for _, kind := range []string{rewards.KindCharacter, rewards.KindOffice} {
		if len(c.Slots(kind)) == 0 {
			t.Errorf("no %s slots", kind)
		}
	}
	// Cosmetic only: every item has a price in coins and nothing else (no gem, no money).
	for _, it := range c.Items {
		if it.Price <= 0 || it.Name.ES == "" || it.Name.EN == "" || it.Description.ES == "" || it.Description.EN == "" {
			t.Errorf("incomplete item %+v", it)
		}
	}
	bad := `{"items":[{"id":"x","kind":"office","category":"c","slot":"s","price":0,"rarity":"common","name":{"es":"a","en":"b"}}]}`
	if _, err := rewards.ParseCatalog([]byte(bad)); err == nil {
		t.Error("an item without price was accepted")
	}
}

func TestEarningRules(t *testing.T) {
	r := rewards.DefaultRules()
	cases := []struct {
		name string
		f    rewards.TaskFacts
		want int64
		why  string
	}{
		{"M unreviewed", rewards.TaskFacts{Work: true, Complexity: "M"}, 10, rewards.ReasonTaskDone},
		{"default complexity", rewards.TaskFacts{Work: true}, 10, rewards.ReasonTaskDone},
		{"S", rewards.TaskFacts{Work: true, Complexity: "s"}, 5, rewards.ReasonTaskDone},
		{"L", rewards.TaskFacts{Work: true, Complexity: "L"}, 20, rewards.ReasonTaskDone},
		{"XL", rewards.TaskFacts{Work: true, Complexity: "XL"}, 40, rewards.ReasonTaskDone},
		{"unknown complexity falls back", rewards.TaskFacts{Work: true, Complexity: "huge"}, 10, rewards.ReasonTaskDone},
		{"first try pass bonus", rewards.TaskFacts{Work: true, Complexity: "M", Reviewed: true, Verdict: "pass"}, 15, rewards.ReasonTaskFirstTry},
		{"pass after rework pays less", rewards.TaskFacts{Work: true, Complexity: "M", Reviewed: true, Verdict: "pass", Reworks: 1}, 5, rewards.ReasonTaskRework},
		{"rework exhausted pays less", rewards.TaskFacts{Work: true, Complexity: "L", Reviewed: true, Verdict: "rework", Exhausted: true, Reworks: 1}, 10, rewards.ReasonTaskRework},
		{"failed review pays nothing", rewards.TaskFacts{Work: true, Complexity: "L", Reviewed: true, Verdict: "fail"}, 0, ""},
		{"milestone or gate is not work", rewards.TaskFacts{Work: false, Complexity: "L"}, 0, ""},
		{"rejected by a human pays nothing", rewards.TaskFacts{Work: true, Complexity: "XL", Rejected: true}, 0, ""},
	}
	for _, c := range cases {
		if got := r.ForTask(c.f); got.Amount != c.want || (c.want > 0 && got.Reason != c.why) {
			t.Errorf("%s: got %+v, want %d %s", c.name, got, c.want, c.why)
		}
	}
	off := r
	off.Enabled = false
	if off.ForTask(rewards.TaskFacts{Work: true}).Amount != 0 || off.ForProject(9) != 0 {
		t.Error("disabled rules still pay")
	}
	if r.ForProject(1) != 0 || r.ForProject(0) != 0 {
		t.Error("a project with fewer than the minimum tasks paid a bonus")
	}
	if got := r.ForProject(5); got != 30 {
		t.Errorf("project(5) = %d", got)
	}
	if got := r.ForProject(500); got != 100 {
		t.Errorf("project bonus is not capped: %d", got)
	}
}

func TestParseRules(t *testing.T) {
	r, err := rewards.ParseRules(`{"base":{"m":12},"rework_pct":25}`)
	if err != nil || r.Base["M"] != 12 || r.Base["L"] != 20 || r.ReworkPct != 25 {
		t.Fatalf("override: %+v %v", r, err)
	}
	for _, bad := range []string{`{`, `{"base":{"XXL":3}}`, `{"base":{"M":-1}}`, `{"default_complexity":"Q"}`, `{"rework_pct":-5}`} {
		if _, err := rewards.ParseRules(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if d, err := rewards.ParseRules(""); err != nil || d.Base["M"] != 10 {
		t.Errorf("defaults: %+v %v", d, err)
	}
}

func TestTaskPaysOnceEvenIfReplayed(t *testing.T) {
	f := newFixture(t)
	tk := done("t1", "analyst", nil)
	for i := 0; i < 5; i++ { // retries, restarts, replayed events
		f.svc.OnTaskCompleted(f.ctx, "org-a", tk)
	}
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w.Balance != 10 || w.EarnedTotal != 10 {
		t.Fatalf("wallet after 5 replays = %+v", w)
	}
	if n := f.cap.count(rewards.EvCoinsEarned); n != 1 {
		t.Fatalf("coins.earned emitted %d times", n)
	}
	led, _ := f.svc.Store.Ledger(f.ctx, "org-a", 10, 0)
	if len(led) != 1 || led[0].RefID != "t1" || led[0].AgentID != "analyst" || led[0].Kind != rewards.KindEarn {
		t.Fatalf("ledger = %+v", led)
	}
	by, _ := f.svc.Store.EarnedByAgent(f.ctx, "org-a")
	if by["analyst"] != 10 {
		t.Fatalf("per-agent = %v", by)
	}
	// The same replay in parallel still pays once.
	var wg sync.WaitGroup
	tk2 := done("t2", "sales", nil)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.svc.OnTaskCompleted(f.ctx, "org-a", tk2) }()
	}
	wg.Wait()
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance != 20 {
		t.Fatalf("parallel replays paid %d", w.Balance)
	}
}

func TestOnlyFinishedWorkPays(t *testing.T) {
	f := newFixture(t)
	for _, st := range []domain.TaskStatus{domain.TaskFailed, domain.TaskBlocked, domain.TaskRunning, domain.TaskPending, domain.TaskAwaitingApproval} {
		tk := done("x-"+string(st), "a", nil)
		tk.Status = st
		f.svc.OnTaskCompleted(f.ctx, "org-a", tk)
	}
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("fail-review", "a", &domain.Review{Verdict: domain.VerdictFail}))
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance != 0 {
		t.Fatalf("unfinished work earned %d", w.Balance)
	}
	// A human rejected an action of the task: no coins.
	f.svc.Rejected = func(_ context.Context, _, taskID, _ string) bool { return taskID == "rej" }
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("rej", "a", nil))
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("ok", "a", nil))
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance != 10 {
		t.Fatalf("balance = %d", w.Balance)
	}
}

type fakeClassifier map[string]struct {
	cx   string
	work bool
}

func (c fakeClassifier) RewardInfo(_ context.Context, _ string, t domain.Task) (string, bool, string) {
	v, ok := c[t.ID]
	if !ok {
		return "", true, ""
	}
	return v.cx, v.work, "p1"
}

func TestComplexityReviewAndGates(t *testing.T) {
	f := newFixture(t)
	f.svc.Classifier = fakeClassifier{
		"big":  {"XL", true},
		"gate": {"L", false},
	}
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("big", "a", &domain.Review{Verdict: domain.VerdictPass}))
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("gate", "a", nil))
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("redo", "a", &domain.Review{Verdict: domain.VerdictPass, Reworks: 1}))
	led, _ := f.svc.Store.Ledger(f.ctx, "org-a", 10, 0)
	got := map[string]int64{}
	for _, e := range led {
		got[e.RefID] = e.Amount
	}
	if len(got) != 2 || got["big"] != 60 || got["redo"] != 5 {
		t.Fatalf("ledger = %v", got)
	}
}

func TestProjectBonusPaysOnce(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.svc.OnProjectDone(f.ctx, "org-a", "proj-1", "Cierre", 6)
	}
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w.Balance != 32 {
		t.Fatalf("project bonus = %d", w.Balance)
	}
}

// The observer pays from the events the orchestrator and the projects publish,
// in the background and never blocking the publisher.
func TestObserverPaysFromEvents(t *testing.T) {
	f := newFixture(t)
	obs := &rewards.Observer{Inner: f.cap}
	obs.Bind(f.svc)
	f.svc.Rec.Pub = obs // coins.earned goes out through the same bus
	ctx := context.Background()
	tk := done("ev1", "analyst", nil)
	for i := 0; i < 3; i++ { // a replayed event
		_ = obs.Publish(ctx, domain.Event{Type: domain.EvTaskCompleted, OrgID: "org-a", Payload: map[string]any{"task": tk}})
	}
	_ = obs.Publish(ctx, domain.Event{Type: "project.status_changed", OrgID: "org-a", Payload: map[string]any{"project": projects.Summary{ID: "p9", Name: "P", Status: "running", TasksDone: 4}}})
	_ = obs.Publish(ctx, domain.Event{Type: "project.status_changed", OrgID: "org-a", Payload: map[string]any{"project": projects.Summary{ID: "p9", Name: "P", Status: "done", TasksDone: 4}}})
	_ = obs.Publish(ctx, domain.Event{Type: "project.status_changed", OrgID: "org-a", Payload: map[string]any{"project": projects.Summary{ID: "p9", Name: "P", Status: "done", TasksDone: 4}}})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance == 10+28 && f.cap.count(rewards.EvCoinsEarned) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a late duplicate would show now
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w.Balance != 38 || f.cap.count(rewards.EvCoinsEarned) != 2 {
		t.Fatalf("balance=%d events=%v", w.Balance, f.cap.types())
	}
	// Other organizations are untouched.
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-b"); w.Balance != 0 {
		t.Fatalf("org-b balance = %d", w.Balance)
	}
	// An observer without a service, or with coins off, is a pass-through.
	var cnt atomic.Int32
	plain := &rewards.Observer{Inner: pubFunc(func(domain.Event) { cnt.Add(1) })}
	_ = plain.Publish(ctx, domain.Event{Type: domain.EvTaskCompleted, Payload: map[string]any{"task": tk}})
	if cnt.Load() != 1 {
		t.Fatal("event lost")
	}
}

type pubFunc func(domain.Event)

func (p pubFunc) Publish(_ context.Context, e domain.Event) error { p(e); return nil }

func earnCoins(t *testing.T, f *fixture, org string, n int) {
	t.Helper()
	for i := 0; i < n/10; i++ {
		f.svc.OnTaskCompleted(application.WithOrg(context.Background(), org), org, done(fmt.Sprintf("%s-seed-%d", org, i), "a", nil))
	}
	if w, _ := f.svc.Store.Wallet(f.ctx, org); w.Balance != int64(n) {
		t.Fatalf("seed balance %d", w.Balance)
	}
}

func TestPurchaseEquipPlace(t *testing.T) {
	f := newFixture(t)
	f.svc.Agents = func(_ context.Context, _, id string) bool { return id == "analyst" }
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_cap", ""); !errors.Is(err, rewards.ErrInsufficient) {
		t.Fatalf("broke purchase: %v", err)
	}
	earnCoins(t, f, "org-a", 200)
	res, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_cap", "k1") // 50
	if err != nil || res.Wallet.Balance != 150 || res.Entry.Kind != rewards.KindSpend || res.Replay {
		t.Fatalf("buy: %+v %v", res, err)
	}
	// Same key again: the original result, no second charge.
	again, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_cap", "k1")
	if err != nil || !again.Replay || again.Wallet.Balance != 150 || again.Entry.ID != res.Entry.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	// Another key for an owned item, and the same key for another item, are refused.
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_cap", "k2"); !errors.Is(err, rewards.ErrAlreadyOwned) {
		t.Fatalf("second copy: %v", err)
	}
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_beanie", "k1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance != 150 || w.SpentTotal != 50 {
		t.Fatalf("wallet = %+v", w)
	}
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "no_such_item", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}

	// Equip needs ownership, a real agent and the right kind.
	if _, err := f.svc.Equip(f.ctx, "org-a", "u1", "analyst", "hat_beanie"); !errors.Is(err, rewards.ErrNotOwned) {
		t.Fatalf("equip not owned: %v", err)
	}
	if _, err := f.svc.Equip(f.ctx, "org-a", "u1", "ghost", "hat_cap"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("equip ghost: %v", err)
	}
	if _, err := f.svc.Equip(f.ctx, "org-a", "u1", "analyst", "plant_cactus"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("equip office item: %v", err)
	}
	eq, err := f.svc.Equip(f.ctx, "org-a", "u1", "analyst", "hat_cap")
	if err != nil || eq.Slot != "hat" {
		t.Fatalf("equip: %+v %v", eq, err)
	}
	// A second hat replaces the first in the slot.
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "hat_beanie", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Equip(f.ctx, "org-a", "u1", "analyst", "hat_beanie"); err != nil {
		t.Fatal(err)
	}
	st, _ := f.svc.State(f.ctx, "org-a")
	if len(st.Equipment) != 1 || st.Equipment[0].ItemID != "hat_beanie" || len(st.Inventory) != 2 {
		t.Fatalf("state = %+v", st)
	}
	if err := f.svc.Unequip(f.ctx, "org-a", "u1", "analyst", "hat"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Unequip(f.ctx, "org-a", "u1", "analyst", "nonsense"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unequip bad slot: %v", err)
	}
	if st, _ := f.svc.State(f.ctx, "org-a"); len(st.Equipment) != 0 {
		t.Fatalf("still equipped: %+v", st.Equipment)
	}

	// Office items.
	if _, err := f.svc.Place(f.ctx, "org-a", "u1", "plant_cactus"); !errors.Is(err, rewards.ErrNotOwned) {
		t.Fatalf("place not owned: %v", err)
	}
	if _, err := f.svc.Buy(f.ctx, "org-a", "u1", "plant_cactus", ""); err != nil {
		t.Fatal(err)
	}
	if p, err := f.svc.Place(f.ctx, "org-a", "u1", "plant_cactus"); err != nil || p.Slot != "plant_1" {
		t.Fatalf("place: %+v %v", p, err)
	}
	if _, err := f.svc.Place(f.ctx, "org-a", "u1", "hat_cap"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("place a hat: %v", err)
	}
	if st, _ := f.svc.State(f.ctx, "org-a"); len(st.Office) != 1 {
		t.Fatalf("office = %+v", st.Office)
	}
	if err := f.svc.Remove(f.ctx, "org-a", "u1", "plant_1"); err != nil {
		t.Fatal(err)
	}

	// Events and audit.
	for _, typ := range []string{rewards.EvShopPurchased, rewards.EvItemEquipped, rewards.EvItemUnequipped, rewards.EvOfficePlaced, rewards.EvOfficeRemoved} {
		if f.cap.count(typ) == 0 {
			t.Errorf("event %s was not published (%v)", typ, f.cap.types())
		}
	}
	if n := f.cap.count(rewards.EvShopPurchased); n != 3 { // the replay did not emit again
		t.Errorf("shop.purchased emitted %d times", n)
	}
	logs, _ := f.mem.AuditChain(f.ctx, "org-a", 0, 100)
	seen := map[string]string{}
	for _, l := range logs {
		seen[l.Action] = l.Actor
	}
	for _, a := range []string{rewards.EvShopPurchased, rewards.EvItemEquipped, rewards.EvItemUnequipped, rewards.EvOfficePlaced, rewards.EvOfficeRemoved} {
		if seen[a] != "u1" {
			t.Errorf("audit %s actor = %q", a, seen[a])
		}
	}
	// Other organizations see none of it.
	if st, _ := f.svc.State(f.ctx, "org-b"); st.Wallet.Balance != 0 || len(st.Inventory) != 0 || len(st.Equipment) != 0 || len(st.Office) != 0 {
		t.Fatalf("org-b state leaks: %+v", st)
	}
}

// Parallel purchases never overspend and never charge one item twice.
func TestConcurrentPurchasesNeverOverspend(t *testing.T) {
	f := newFixture(t)
	earnCoins(t, f, "org-a", 130)
	// 63 items; the wallet holds 130 coins. Everybody tries to buy everything at once.
	var wg sync.WaitGroup
	var spent atomic.Int64
	for round := 0; round < 3; round++ { // the same items from three clients
		for _, it := range f.svc.Catalog.Items {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := f.svc.Buy(f.ctx, "org-a", "u", it.ID, ""); err == nil && !r.Replay {
					spent.Add(r.Entry.Amount)
				}
			}()
		}
	}
	wg.Wait()
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w.Balance < 0 || w.Balance+w.SpentTotal != 130 || w.SpentTotal != spent.Load() {
		t.Fatalf("wallet = %+v, spent by callers %d", w, spent.Load())
	}
	inv, _ := f.svc.Store.Inventory(f.ctx, "org-a")
	var paid int64
	for _, o := range inv {
		paid += o.PricePaid
	}
	if paid != w.SpentTotal {
		t.Fatalf("inventory worth %d, wallet spent %d", paid, w.SpentTotal)
	}
	led, _ := f.svc.Store.Ledger(f.ctx, "org-a", 500, 0)
	seen := map[int64]bool{}
	for _, e := range led {
		if seen[e.Seq] {
			t.Fatalf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
		if e.BalanceAfter < 0 {
			t.Fatalf("negative balance in the ledger: %+v", e)
		}
	}
}

func TestLedgerPagingNewestFirst(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 7; i++ {
		f.svc.OnTaskCompleted(f.ctx, "org-a", done(fmt.Sprintf("t%d", i), "a", nil))
	}
	p1, _ := f.svc.Store.Ledger(f.ctx, "org-a", 3, 0)
	p2, _ := f.svc.Store.Ledger(f.ctx, "org-a", 3, p1[len(p1)-1].Seq)
	if len(p1) != 3 || p1[0].Seq != 7 || len(p2) != 3 || p2[0].Seq != 4 || p2[0].BalanceAfter != 40 {
		t.Fatalf("pages: %+v / %+v", p1, p2)
	}
}

// No code path credits coins other than the earning rules: the Service has no
// "grant", "topup" or "add" method, and the REST API has no such route (see the api tests).
func TestRewardsDisabledPaysNothing(t *testing.T) {
	f := newFixture(t)
	f.svc.Rules.Enabled = false
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("t", "a", nil))
	f.svc.OnProjectDone(f.ctx, "org-a", "p", "P", 9)
	if w, _ := f.svc.Store.Wallet(f.ctx, "org-a"); w.Balance != 0 {
		t.Fatalf("balance = %d", w.Balance)
	}
}
