package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/quality"
)

// defaultInitiativeScan is how often the metric signals of proactive employees are evaluated.
const defaultInitiativeScan = 15 * time.Minute

// wireInitiativeScanner builds the metric signals of proactive employees (docs/architecture/initiative.md): sheet and
// table artifacts, the spend pace against the organization cap, aging approvals and the quality score. Each source
// is optional. The kill switch or a paused agent keeps that agent quiet (controls.Check without side effects).
func wireInitiativeScanner(svc *application.InitiativeService, store application.Store, orch *application.Orchestrator,
	arts *artifacts.Service, q *quality.Service, ctl *controls.Service, orgCfg *application.OrgConfig) *application.InitiativeScanner {
	if svc == nil {
		return nil
	}
	sc := &application.InitiativeScanner{Svc: svc, Core: store}
	if b := orch.Budget(); b != nil {
		sc.OrgCap = b.OrgCap
	}
	if arts != nil {
		sc.Series = arts.MetricSeries
	}
	if q != nil {
		sc.Quality = q.Changes
	}
	if ctl != nil {
		sc.Allowed = func(ctx context.Context, org, agentID string) bool {
			return ctl.Check(ctx, org, agentID, false).Allowed
		}
	}
	if orgCfg != nil {
		sc.Locale = func(ctx context.Context) string { return orgCfg.Locale(ctx, "") }
	}
	return sc
}

// initiativeScanInterval reads INITIATIVE_SCAN_INTERVAL (Go duration; "0" or "off" turns the periodic scan off,
// POST /initiatives/scan keeps working). Invalid values fall back to the default.
func initiativeScanInterval(log *slog.Logger) time.Duration {
	v := strings.TrimSpace(os.Getenv("INITIATIVE_SCAN_INTERVAL"))
	switch v {
	case "":
		return defaultInitiativeScan
	case "0", "off", "false":
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		log.Warn("INITIATIVE_SCAN_INTERVAL must be a duration of at least 1m; using the default", "value", v)
		return defaultInitiativeScan
	}
	return d
}

// startInitiativeScan evaluates every organization's signals once per interval. The first sweep waits a whole
// interval, so a fresh boot (and the end-to-end suite) never starts with suggestions it did not cause.
func startInitiativeScan(ctx context.Context, log *slog.Logger, sc *application.InitiativeScanner, store application.Store, fallbackOrg string, every time.Duration) {
	if sc == nil || every <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			orgs := []string{fallbackOrg}
			if l, ok := store.(application.OrgLister); ok {
				if ids, err := l.ListOrgIDs(ctx); err == nil {
					orgs = ids
				} else {
					log.Warn("initiative scan: list organizations", "err", err)
				}
			}
			for _, org := range orgs {
				if ctx.Err() != nil {
					return
				}
				octx := application.WithActor(application.WithOrg(ctx, org), "system")
				if _, err := sc.Scan(octx); err != nil {
					log.Warn("initiative scan", "org", org, "err", err)
				}
			}
		}
	}()
}
