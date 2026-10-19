package main

import (
	"context"
	"time"
	"crypto/hmac"
	"crypto/sha256"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/mailer"
	"aiworkforce/backend/internal/notify"
)

// wireNotify builds the approval notifications (e-mail, Slack, Teams), the
// signed approval links and the plan with a hard spend cap
// (docs/architecture/approvals-notify.md).
//
//	NOTIFY_PUBLIC_URL    public base of the API for the links (default UI_BASE_URL); without it no links are offered
//	NOTIFY_LINK_SECRET   signing key, >= 32 bytes (default: derived from JWT_SECRET, never the same key)
//	NOTIFY_LINK_TTL_HOURS validity of a link (default 24, max 168)
//	FREE_PLAN_CAP_USD    > 0 makes every organization without an explicit plan a free one with this HARD cap
func wireNotify(log *slog.Logger, cfg config.Config, pg *postgres.Store, store application.Store, approvals *application.Approvals,
	orch *application.Orchestrator, obs *notify.Observer) *notify.Service {
	var settings notify.SettingsStore = notify.NewMemSettings()
	var replay notify.ReplayStore = notify.NewMemReplay()
	var planStore notify.PlanStore = notify.NewMemPlans()
	if pg != nil {
		db := &notify.PG{DB: pg}
		settings, replay, planStore = db, db, db
	}
	freeCap := 0.0
	if v := strings.TrimSpace(os.Getenv("FREE_PLAN_CAP_USD")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			freeCap = f
		} else {
			log.Warn("FREE_PLAN_CAP_USD ignored: it must be a number greater than 0")
		}
	}
	plans := &notify.Plans{Store: planStore, DefaultFreeCapUSD: freeCap}
	// The free plan cap is enforced where every call is reserved (Budget.Reserve): never silent, never over.
	orch.Budget().OrgCapFor = plans.OrgCap
	if freeCap > 0 {
		log.Info("free plan with a hard spend cap is ON", "cap_usd", freeCap)
	}

	svc := &notify.Service{Settings: settings, Replay: replay, Plans: plans, Log: log,
		Approvals: notify.ApprovalsAdapter{Approvals: approvals, Store: store},
		PublicURL: strings.TrimRight(firstNonEmpty(os.Getenv("NOTIFY_PUBLIC_URL"), cfg.UIBaseURL), "/"),
		AppURL:    strings.TrimRight(cfg.UIBaseURL, "/"),
		AgentName: func(ctx context.Context, org, id string) string {
			if a, err := store.GetAgent(ctx, org, id); err == nil {
				return a.Name
			}
			return ""
		},
	}
	if h, err := strconv.Atoi(strings.TrimSpace(os.Getenv("NOTIFY_LINK_TTL_HOURS"))); err == nil && h > 0 {
		svc.TTL = time.Duration(h) * time.Hour
	}
	key := []byte(os.Getenv("NOTIFY_LINK_SECRET"))
	if len(key) == 0 && len(cfg.JWTSecret) >= 16 {
		m := hmac.New(sha256.New, []byte(cfg.JWTSecret))
		m.Write([]byte("aiworkforce/notify-links/v1"))
		key = m.Sum(nil)
	}
	if sg, err := notify.NewSigner(key); err == nil {
		svc.Signer = sg
	} else {
		log.Info("approval links disabled (set NOTIFY_LINK_SECRET with at least 32 bytes)")
	}
	switch {
	case cfg.EmailDevLog:
		svc.Mail = mailer.Log{Log: log}
	case cfg.SMTP.Host != "":
		if sm, err := mailer.NewSMTP(cfg.SMTP); err == nil {
			svc.Mail = sm
		} else {
			log.Warn("approval e-mails disabled: invalid SMTP configuration", "err", err)
		}
	}
	if svc.PublicURL == "" {
		log.Info("approval links disabled (NOTIFY_PUBLIC_URL / UI_BASE_URL not set)")
	}
	obs.Bind(svc)
	obs.UsageOf = func(ctx context.Context, org string) (notify.Usage, bool) {
		st, err := orch.Budget().Status(application.WithOrg(ctx, org))
		if err != nil {
			return notify.Usage{}, false
		}
		return plans.Status(ctx, org, cfg.App.BudgetUSD, st.Org.UsedUSD, st.Org.ReservedUSD), true
	}
	return svc
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
