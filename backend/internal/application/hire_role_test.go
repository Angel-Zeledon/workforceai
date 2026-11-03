package application_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

func TestHireForRoleUsesTheSeedIdentityWhenItIsFree(t *testing.T) {
	// The memory store holds one office: start from an empty one with only the assistant, like a new organization.
	cfg := application.DefaultConfig()
	store := memory.New()
	ctx := application.WithOrg(context.Background(), cfg.OrgID)
	if err := store.Seed(ctx, domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgentsFor([]string{"assistant"}, "es")); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: &capture{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	orch := application.NewOrchestrator(ctx, cfg, store, &fakeRuntime{}, memory.NewLocker(), rec, appr, &application.Queries{Store: store, Cfg: cfg}, log)
	h := &harness{t: t, store: store, orch: orch}
	a, err := h.orch.HireForRole(ctx, "legal", "es")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "legal" || a.Name != "Elena Castro" || a.Role != "legal" {
		t.Fatalf("legal = %+v", a)
	}
	b, err := h.orch.HireForRole(ctx, "legal", "es")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == "legal" || b.Role != "legal" {
		t.Fatalf("a second lawyer needs another id: %+v", b)
	}
	pm, err := h.orch.HireForRole(ctx, "project_manager", "en")
	if err != nil || pm.ID != "project_manager" || pm.Name == "" {
		t.Fatalf("project manager = %+v %v", pm, err)
	}
	all, _ := h.store.ListAgents(ctx, cfg.OrgID)
	if len(all) != 4 {
		t.Fatalf("agents = %d, want 4", len(all))
	}
	if _, err := h.orch.HireForRole(ctx, "astronaut", "es"); err == nil {
		t.Fatal("unknown role must fail")
	}
}
