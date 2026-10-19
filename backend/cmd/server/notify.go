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
	"aiworkforce/backend/internal/domain"
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
	var outbox notify.OutboxStore = notify.NewMemOutbox()
	if pg != nil {
		db := &notify.PG{DB: pg, Log: log}
		settings, replay, planStore, outbox = db, db, db, db
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

	svc := &notify.Service{Settings: settings, Replay: replay, Plans: plans, Log: log, Outbox: outbox,
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
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("NOTIFY_OUTBOX"))); v == "off" || v == "false" || v == "0" {
		svc.Outbox = nil // messages are sent once, inline, as before migration 784
		log.Warn("NOTIFY_OUTBOX=off: notifications are not queued and failed ones are not retried")
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

// startNotifyJobs binds the vault (it exists after wireNotify) and starts the background jobs of the notifications:
// the outbox worker (retries) and the monthly reset of the plan caps (PlanResetter). The vault seals the Slack/Teams
// URLs and the queued payloads; without a key they stay in the clear, as before migrations 783 and 784.
//
//	NOTIFY_OUTBOX=off   send notifications once, inline, without the retry queue
//	PLAN_RESET_JOB=off  do not reset the plan caps on the 1st of the month
func startNotifyJobs(ctx context.Context, log *slog.Logger, svc *notify.Service, sealer notify.Sealer, lister application.OrgLister,
	orch *application.Orchestrator, rec *application.Recorder) {
	svc.Sealer = sealer
	if db, ok := svc.Settings.(*notify.PG); ok {
		db.Sealer = sealer
	}
	if lister == nil {
		return
	}
	svc.StartOutbox(ctx, lister)
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_RESET_JOB"))); v == "off" || v == "false" || v == "0" {
		log.Warn("PLAN_RESET_JOB=off: plan caps are not reset on the 1st of the month")
		return
	}
	r := &notify.PlanResetter{Plans: svc.Plans, Orgs: lister, Log: log,
		Spend: func(c context.Context, org string) (float64, error) {
			st, err := orch.Budget().Status(application.WithOrg(c, org))
			return st.Org.UsedUSD, err
		},
		Audit: func(c context.Context, org, action string, d map[string]any) {
			rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: "system", Action: action, Entity: "plan", EntityID: org, Details: d})
		}}
	r.Start(ctx, time.Hour)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
