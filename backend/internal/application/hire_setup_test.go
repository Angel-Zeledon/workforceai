package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// A hire with a setup step is all or nothing: when the setup fails the agent is gone again and nobody was told.
func TestHireWithSetupRollsBackWhenSetupFails(t *testing.T) {
	cfg := application.DefaultConfig()
	store := memory.New()
	ctx := application.WithOrg(context.Background(), cfg.OrgID)
	if err := store.Seed(ctx, domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgentsFor([]string{"assistant"}, "es")); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &capture{}
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	orch := application.NewOrchestrator(ctx, cfg, store, &fakeRuntime{}, memory.NewLocker(), rec, appr, &application.Queries{Store: store, Cfg: cfg}, log)
	count := func() int { as, _ := store.ListAgents(ctx, cfg.OrgID); return len(as) }
	n := count()

	boom := errors.New("boom")
	seen := ""
	_, err := orch.HireWithSetup(ctx, application.HireInput{TemplateID: "legal", Locale: "es"}, func(_ context.Context, a domain.Agent) error {
		seen = a.ID
		if count() != n+1 {
			t.Fatal("setup must see the new agent")
		}
		return boom
	})
	if !errors.Is(err, boom) || seen == "" {
		t.Fatalf("err = %v, seen %q", err, seen)
	}
	if count() != n {
		t.Fatalf("the failed hire left an agent: %d -> %d", n, count())
	}
	events := len(pub.events)
	// the same hire works afterwards and takes the same id: nothing was left behind
	a, err := orch.HireWithSetup(ctx, application.HireInput{TemplateID: "legal", Locale: "es"}, func(context.Context, domain.Agent) error { return nil })
	if err != nil || a.ID != seen || count() != n+1 {
		t.Fatalf("hire = %+v %v (rolled back id %q)", a, err, seen)
	}
	if len(pub.events) <= events {
		t.Fatal("a completed hire announces the agent")
	}
}
