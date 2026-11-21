package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
)

// defaultWatchdogScan is how often the stalled-work watchdog sweeps every organization.
const defaultWatchdogScan = 5 * time.Minute

// wireWatchdog builds the stalled-work watchdog (docs/architecture/watchdog.md): thresholds and suggestions through
// the initiative service (nil: defaults, no suggestions), the stored text in the organization's language. Accepting a
// stalled-task suggestion retries the task.
func wireWatchdog(orch *application.Orchestrator, svc *application.InitiativeService, rec *application.Recorder, cfg application.Config,
	orgCfg *application.OrgConfig) *application.Watchdog {
	w := &application.Watchdog{Orch: orch, Initiative: svc, Rec: rec, Cfg: cfg}
	if orgCfg != nil {
		w.Locale = func(ctx context.Context) string { return orgCfg.Locale(ctx, "") }
	}
	w.Install()
	return w
}

// watchdogInterval reads WATCHDOG_INTERVAL (Go duration, at least 1m; "0" or "off" turns the sweep off while the
// endpoints keep working). Invalid values fall back to the default.
func watchdogInterval(log *slog.Logger) time.Duration {
	v := strings.TrimSpace(os.Getenv("WATCHDOG_INTERVAL"))
	switch v {
	case "":
		return defaultWatchdogScan
	case "0", "off", "false":
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		log.Warn("WATCHDOG_INTERVAL must be a duration of at least 1m; using the default", "value", v)
		return defaultWatchdogScan
	}
	return d
}

// startWatchdog sweeps every organization once per interval. The first sweep waits a whole interval: right after a
// boot, Recover is still resuming requests and nothing has had time to stall.
func startWatchdog(ctx context.Context, log *slog.Logger, w *application.Watchdog, store application.Store, fallbackOrg string, every time.Duration) {
	if w == nil || every <= 0 {
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
			orgs := application.WithDefaultOrg(nil, fallbackOrg)
			if l, ok := store.(application.OrgLister); ok {
				if ids, err := l.ListOrgIDs(ctx); err == nil {
					orgs = application.WithDefaultOrg(ids, fallbackOrg)
				} else {
					log.Warn("watchdog: list organizations", "err", err)
				}
			}
			for _, org := range orgs {
				if ctx.Err() != nil {
					return
				}
				octx := application.WithActor(application.WithOrg(ctx, org), "system")
				if _, err := w.Scan(octx); err != nil {
					log.Warn("watchdog scan", "org", org, "err", err)
				}
			}
		}
	}()
}
