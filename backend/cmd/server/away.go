package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// wireAway builds the "while you were away" summary (docs/architecture/away-summary.md). The summary reads the
// event log, pending approvals and the cost ledger of the application store; with Postgres the last-seen marks
// live in migration 792, without it in memory. AWAY_SUMMARY_MIN_HOURS changes the default absence (8 hours)
// after which the frontend opens the summary on its own. Returns nil (endpoints not mounted) when the store
// cannot answer the history queries.
func wireAway(cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store) *away.Service {
	hist, ok := store.(away.History)
	if !ok {
		log.Warn("away summary disabled: the store does not expose the event log")
		return nil
	}
	acfg := away.Config{History: hist, OrgID: cfg.App.OrgID, Log: log}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("AWAY_SUMMARY_MIN_HOURS")), 64); err == nil && v >= 0 && v <= away.MaxMinHours {
		acfg.MinHours = v
	}
	if pg != nil {
		acfg.Marks = pg
	} else {
		acfg.Marks = away.NewMemMarks()
	}
	return away.New(acfg)
}
