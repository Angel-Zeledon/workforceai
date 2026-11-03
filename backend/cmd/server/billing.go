package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/billing"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/notify"
)

// wireBilling charges the plans with ONVO Pay (docs/architecture/billing-onvo.md). Off unless ONVO_SECRET_KEY:
//
//	ONVO_SECRET_KEY        onvo_test_secret_key_... / onvo_live_secret_key_...
//	ONVO_PUBLISHABLE_KEY   onvo_test_publishable_key_... (same mode): the browser's payment form uses it
//	ONVO_WEBHOOK_SECRET    the webhook's secret (webhook_secret_..., ONVO dashboard > Developers > Webhooks)
//	ONVO_PRICE_PRO_MONTHLY, ONVO_PRICE_MAX_MONTHLY   ids of the recurring prices (deploy/onvo-setup.sh prints them)
//	ONVO_PRICE_PRO_YEARLY, ONVO_PRICE_MAX_YEARLY     optional: without them only monthly is offered
//	ONVO_API_BASE          optional (tests)
//
// It needs the plans (PLANS=on). Half-configured, the server refuses to start: it could take money and never move a
// plan, or sell with a test key in production.
func wireBilling(log *slog.Logger, svc *notify.Service, rec *application.Recorder) (*billing.Service, error) {
	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	key := env("ONVO_SECRET_KEY")
	if key == "" {
		return nil, nil
	}
	pub, secret := env("ONVO_PUBLISHABLE_KEY"), env("ONVO_WEBHOOK_SECRET")
	prices := map[string]string{}
	for _, k := range []string{"pro_monthly", "pro_yearly", "max_monthly", "max_yearly"} {
		if v := env("ONVO_PRICE_" + strings.ToUpper(k)); v != "" {
			prices[k] = v
		}
	}
	live := strings.HasPrefix(key, "onvo_live_")
	switch {
	case !strings.HasPrefix(key, "onvo_test_secret_key_") && !strings.HasPrefix(key, "onvo_live_secret_key_"):
		return nil, errors.New("ONVO_SECRET_KEY must be an ONVO secret key (onvo_test_secret_key_... or onvo_live_secret_key_...)")
	case pub == "":
		return nil, errors.New("ONVO_SECRET_KEY is set but ONVO_PUBLISHABLE_KEY is not: the payment form cannot load")
	case strings.HasPrefix(pub, "onvo_live_") != live:
		return nil, errors.New("ONVO_SECRET_KEY and ONVO_PUBLISHABLE_KEY are of different modes (test and live)")
	case secret == "":
		return nil, errors.New("ONVO_SECRET_KEY is set but ONVO_WEBHOOK_SECRET is not")
	case prices["pro_monthly"] == "" || prices["max_monthly"] == "":
		return nil, errors.New("ONVO_PRICE_PRO_MONTHLY and ONVO_PRICE_MAX_MONTHLY are required (deploy/onvo-setup.sh)")
	case svc == nil || svc.Plans == nil || (!envOn("PLANS") && os.Getenv("FREE_PLAN_CAP_USD") == ""):
		return nil, errors.New("ONVO billing needs the plans: set PLANS=on")
	}
	if live {
		log.Info("ONVO billing on (LIVE mode: real charges)")
	} else {
		log.Info("ONVO billing on (test mode)")
	}
	return &billing.Service{Onvo: &billing.Onvo{Key: key, Base: env("ONVO_API_BASE")}, PublishableKey: pub, WebhookSecret: secret,
		Prices: prices, Plans: svc.Plans, Log: log,
		Audit: func(ctx context.Context, org, action string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: "billing", Action: action, Entity: "plan", EntityID: org, Details: d})
		}}, nil
}

// startBillingJob syncs every organization billed by ONVO every 15 minutes: ONVO sends no event when a subscription
// is cancelled, and a first payment may land after the browser was closed. BILLING_SYNC_JOB=off turns it off.
func startBillingJob(ctx context.Context, log *slog.Logger, svc *billing.Service, lister application.OrgLister) {
	if svc == nil || lister == nil {
		return
	}
	if v := strings.ToLower(os.Getenv("BILLING_SYNC_JOB")); v == "off" || v == "false" || v == "0" {
		log.Warn("BILLING_SYNC_JOB=off: cancelled subscriptions only reach the plan through webhooks")
		return
	}
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ids, err := lister.ListOrgIDs(ctx)
				if err != nil {
					log.Warn("billing sync: cannot list organizations", "err", err)
					continue
				}
				if n, errs := svc.Reconcile(ctx, ids); n > 0 || errs > 0 {
					log.Info("billing sync", "synced", n, "errors", errs)
				}
			}
		}
	}()
}
