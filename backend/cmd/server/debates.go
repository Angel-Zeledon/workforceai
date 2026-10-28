package main

import (
	"context"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
)

// wireDebates builds the team debate service (B5, docs/architecture/team-debates.md) and hooks it into the
// orchestrator. Caps: DEBATE_MAX_TURNS (default 8, clamped 3..12) and DEBATE_MAX_COST_USD (default 0.10).
// DEBATES_ENABLED=false keeps plans exactly as before (no debate before a flagged decision; /debates unmounted).
// Returns nil when the store cannot keep debates.
func wireDebates(store application.Store, rt any, orch *application.Orchestrator, rec *application.Recorder, cfg application.Config, orgCfg *application.OrgConfig) *application.Debates {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("DEBATES_ENABLED"))); v == "false" || v == "0" || v == "off" {
		return nil
	}
	ds, ok := store.(application.DebateStore)
	if !ok {
		return nil
	}
	svc := &application.Debates{Store: ds, Agents: store, Runtime: rt, Budget: orch, Rec: rec, Cfg: cfg}
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DEBATE_MAX_TURNS"))); err == nil {
		svc.MaxTurns = n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("DEBATE_MAX_COST_USD")), 64); err == nil && f > 0 {
		svc.MaxCostUSD = f
	}
	if orgCfg != nil {
		svc.Locale = func(c context.Context) string { return orgCfg.Locale(c, "") }
	}
	orch.SetDebates(svc)
	return svc
}
