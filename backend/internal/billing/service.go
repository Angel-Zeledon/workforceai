package billing

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/policy"
)

// Provider is the value of org_plans.provider for a plan charged by ONVO.
const Provider = "onvo"

// App marks this app's subscriptions (metadata "app") in an ONVO account that may be shared with other apps.
const App = "workforceai"

// Periods a plan can be paid for.
const (
	Monthly = "monthly"
	Yearly  = "yearly"
)

// PriceKey is the key of a price in Service.Prices: "pro_monthly", "max_yearly"...
func PriceKey(plan, period string) string { return plan + "_" + period }

var (
	// ErrBadPlan: only the paid plans of the catalog with a configured price can be bought.
	ErrBadPlan = errors.New("billing: unknown paid plan or period, or no price configured for it")
	// ErrSamePlan: the organization already pays that plan.
	ErrSamePlan = errors.New("billing: the organization is already on that plan")
	// ErrNotPaid: the subscription exists but its first payment is not confirmed (yet).
	ErrNotPaid = errors.New("billing: the subscription is not paid yet")
	// ErrNoSubscription: there is no subscription to cancel or resume.
	ErrNoSubscription = errors.New("billing: the organization has no subscription")
	// ErrForeign: the subscription is not this organization's.
	ErrForeign = errors.New("billing: the subscription is not this organization's")
	// ErrWebhookSecret: the webhook did not carry the endpoint's secret.
	ErrWebhookSecret = errors.New("billing: bad webhook secret")
)

// PlanBook is what billing needs of the plans (notify.Plans).
type PlanBook interface {
	Resolve(ctx context.Context, org string) notify.Plan
	Change(ctx context.Context, org, plan, by string) (notify.Plan, error)
	SetBilling(ctx context.Context, org string, b notify.Billing) error
}

// Service sells the plans with ONVO and keeps org_plans in step with it.
type Service struct {
	Onvo           *Onvo
	PublishableKey string            // ONVO_PUBLISHABLE_KEY: the browser's SDK pays with it
	WebhookSecret  string            // ONVO_WEBHOOK_SECRET: the X-Webhook-Secret of the endpoint (webhook_secret_...)
	Prices         map[string]string // PriceKey -> ONVO price id
	Plans          PlanBook
	// Audit (optional) records plan changes made by billing.
	Audit func(ctx context.Context, org, action string, details map[string]any)
	Log   *slog.Logger
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func billingOf(p notify.Plan) notify.Billing {
	if p.Provider != Provider {
		return notify.Billing{Provider: Provider}
	}
	return notify.Billing{Provider: Provider, Customer: p.ProviderRef, Subscription: p.ProviderSub, Pending: p.ProviderPending}
}

// live are the statuses that keep a plan. past_due too: ONVO is still retrying the payment; when it gives up the
// subscription is canceled or unpaid and the plan goes back to Free.
var live = map[string]bool{"active": true, "trialing": true, "past_due": true}

// dead are the statuses of a subscription that will never be paid again.
var dead = map[string]bool{"canceled": true, "unpaid": true, "incomplete_expired": true}

// planOf is the plan a subscription sells: its price, or the "plan" metadata it was created with.
func (s *Service) planOf(sub Subscription) string {
	for _, id := range sub.PriceIDs {
		for k, v := range s.Prices {
			if v == id {
				for _, t := range policy.Catalog {
					if k == PriceKey(t.ID, Monthly) || k == PriceKey(t.ID, Yearly) {
						return t.ID
					}
				}
			}
		}
	}
	if p := sub.Metadata["plan"]; policy.IsCatalogPlan(p) && p != policy.PlanFree {
		return p
	}
	return ""
}

// Available are the price keys that can be bought ("pro_monthly", ...).
func (s *Service) Available() []string {
	var out []string
	for _, t := range policy.Catalog {
		for _, per := range []string{Monthly, Yearly} {
			if s.Prices[PriceKey(t.ID, per)] != "" && t.PriceMonthlyUSD > 0 {
				out = append(out, PriceKey(t.ID, per))
			}
		}
	}
	return out
}

// Status is what the plan screen shows about the subscription.
type Status struct {
	Subscribed        bool       `json:"subscribed"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	PeriodEnd         *time.Time `json:"period_end,omitempty"`
	PastDue           bool       `json:"past_due"`
}

// StatusOf reads the organization's subscription from ONVO (best effort: ONVO down = only what is stored).
func (s *Service) StatusOf(ctx context.Context, org string) Status {
	b := billingOf(s.Plans.Resolve(ctx, org))
	if b.Subscription == "" {
		return Status{}
	}
	st := Status{Subscribed: true}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if sub, err := s.Onvo.GetSubscription(cctx, b.Subscription); err == nil {
		st.CancelAtPeriodEnd, st.PeriodEnd, st.PastDue = sub.CancelAtPeriodEnd, sub.PeriodEnd, sub.Status == "past_due"
	}
	return st
}

// Started is a subscription waiting for its first payment: the browser pays it with ONVO's SDK.
type Started struct {
	SubscriptionID string `json:"subscription_id"`
	CustomerID     string `json:"customer_id"`
	PublishableKey string `json:"publishable_key"`
}

// Start creates the subscription of a purchase or a plan change (to be paid in the browser). The plan does not
// change until Confirm sees it paid.
func (s *Service) Start(ctx context.Context, org, plan, period, name, email string) (Started, error) {
	price := s.Prices[PriceKey(plan, period)]
	if t, ok := policy.TierOf(plan); !ok || t.PriceMonthlyUSD <= 0 || price == "" {
		return Started{}, ErrBadPlan
	}
	cur := s.Plans.Resolve(ctx, org)
	b := billingOf(cur)
	if cur.Plan == plan && b.Subscription != "" {
		// The same plan again only to replace a subscription whose payment is failing (another card).
		if sub, err := s.Onvo.GetSubscription(ctx, b.Subscription); err != nil || sub.Status != "past_due" {
			return Started{}, ErrSamePlan
		}
	}
	if b.Customer == "" {
		id, err := s.Onvo.CreateCustomer(ctx, name, email)
		if err != nil {
			return Started{}, err
		}
		b.Customer = id
	}
	sub, err := s.Onvo.CreateSubscription(ctx, b.Customer, price,
		map[string]string{"app": App, "org": org, "plan": plan, "period": period})
	if err != nil {
		return Started{}, err
	}
	// Leave a pending subscription that was never paid: ONVO expires it; only the newest is followed.
	b.Pending = sub.ID
	if err := s.ensureRow(ctx, org, cur); err != nil {
		return Started{}, err
	}
	if err := s.Plans.SetBilling(ctx, org, b); err != nil {
		return Started{}, err
	}
	return Started{SubscriptionID: sub.ID, CustomerID: b.Customer, PublishableKey: s.PublishableKey}, nil
}

// ensureRow makes sure org has a plan row (SetBilling updates it): an organization on the deployment default has none.
func (s *Service) ensureRow(ctx context.Context, org string, cur notify.Plan) error {
	if cur.Provider != "" {
		return nil
	}
	plan := cur.Plan
	if !policy.IsCatalogPlan(plan) {
		plan = policy.PlanFree
	}
	_, err := s.Plans.Change(ctx, org, plan, "billing")
	return err
}

// Confirm moves org to the plan of subscription id once ONVO says it is paid (the browser calls it after the SDK's
// onSuccess; Sync does it too, for a browser closed too early). The previous subscription is cancelled FIRST, so a
// failure never leaves two plans charging. Calling it again changes nothing.
func (s *Service) Confirm(ctx context.Context, org, id string) error {
	cur := s.Plans.Resolve(ctx, org)
	b := billingOf(cur)
	if id == "" || (id != b.Pending && id != b.Subscription) {
		return ErrForeign
	}
	if id == b.Subscription { // already done
		return nil
	}
	sub, err := s.Onvo.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if sub.Metadata["app"] != App || sub.Metadata["org"] != org || (sub.Customer != "" && sub.Customer != b.Customer) {
		return ErrForeign
	}
	if sub.Status != "active" && sub.Status != "trialing" {
		if dead[sub.Status] { // never paid and never will be: forget it
			b.Pending = ""
			_ = s.Plans.SetBilling(ctx, org, b)
		}
		return ErrNotPaid
	}
	plan := s.planOf(sub)
	if plan == "" {
		return ErrForeign
	}
	if old := b.Subscription; old != "" && old != id {
		if err := s.Onvo.CancelNow(ctx, old); err != nil && !NotFound(err) {
			return err
		}
	}
	if _, err := s.Plans.Change(ctx, org, plan, "billing"); err != nil {
		return err
	}
	b.Subscription, b.Pending = id, ""
	if err := s.Plans.SetBilling(ctx, org, b); err != nil {
		return err
	}
	s.audit(ctx, org, cur.Plan, plan, "paid", id)
	return nil
}

// Cancel ends the subscription when the paid period ends (resume=false), or undoes that (resume=true). The plan
// stays until then; Sync moves it to Free when ONVO cancels the subscription.
func (s *Service) Cancel(ctx context.Context, org string, resume bool) error {
	b := billingOf(s.Plans.Resolve(ctx, org))
	if b.Subscription == "" {
		return ErrNoSubscription
	}
	_, err := s.Onvo.SetCancelAtPeriodEnd(ctx, b.Subscription, !resume)
	if err == nil && s.Audit != nil {
		action := "plan.cancel_requested"
		if resume {
			action = "plan.cancel_withdrawn"
		}
		s.Audit(ctx, org, action, map[string]any{"subscription": b.Subscription})
	}
	return err
}

// Sync puts org where ONVO says it is: a pending subscription that got paid is confirmed, and the plan follows the
// subscription in force (Free when it was cancelled or will never be paid). Repeating it changes nothing.
func (s *Service) Sync(ctx context.Context, org string) error {
	cur := s.Plans.Resolve(ctx, org)
	b := billingOf(cur)
	if cur.Provider != Provider {
		return nil
	}
	if b.Pending != "" {
		if err := s.Confirm(ctx, org, b.Pending); err != nil && !errors.Is(err, ErrNotPaid) && !errors.Is(err, ErrForeign) {
			return err
		}
		cur = s.Plans.Resolve(ctx, org)
		b = billingOf(cur)
	}
	if b.Subscription == "" {
		return nil
	}
	sub, err := s.Onvo.GetSubscription(ctx, b.Subscription)
	if err != nil && !NotFound(err) {
		return err
	}
	switch {
	case err == nil && live[sub.Status]:
		if p := s.planOf(sub); p != "" && p != cur.Plan {
			if _, err := s.Plans.Change(ctx, org, p, "billing"); err != nil {
				return err
			}
			s.audit(ctx, org, cur.Plan, p, "synced", b.Subscription)
		}
		return nil
	case err != nil || dead[sub.Status]: // gone, cancelled, or ONVO gave up charging it
		if _, err := s.Plans.Change(ctx, org, policy.PlanFree, "billing"); err != nil {
			return err
		}
		ended := b.Subscription
		b.Subscription = ""
		if err := s.Plans.SetBilling(ctx, org, b); err != nil {
			return err
		}
		s.audit(ctx, org, cur.Plan, policy.PlanFree, "ended", ended)
	}
	return nil
}

func (s *Service) audit(ctx context.Context, org, from, to, why, sub string) {
	if from == to {
		return
	}
	s.log().Info("plan changed by billing", "org", org, "from", from, "to", to, "why", why, "subscription", sub)
	if s.Audit != nil {
		s.Audit(ctx, org, "plan.changed", map[string]any{"from": from, "to": to, "via": Provider, "why": why, "subscription": sub})
	}
}

// HandleWebhook checks the endpoint's secret and syncs the organization of the subscription the event is about.
// ErrWebhookSecret means "not from ONVO" (answer 401); another error means "try again" (answer 500). Events that do not
// name a subscription of this app are acknowledged and ignored (Sync's job catches the rest).
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, secret string) error {
	if s.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(s.WebhookSecret)) != 1 {
		return ErrWebhookSecret
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			SubscriptionID string            `json:"subscriptionId"`
			Metadata       map[string]string `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil
	}
	id := ev.Data.SubscriptionID
	if id == "" {
		return nil
	}
	// The event's own fields are not trusted: the subscription is read from ONVO.
	sub, err := s.Onvo.GetSubscription(ctx, id)
	if err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	if sub.Metadata["app"] != App || sub.Metadata["org"] == "" {
		return nil
	}
	return s.Sync(ctx, sub.Metadata["org"])
}

// Reconcile syncs every organization billed by ONVO (a job: ONVO sends no event when a subscription is cancelled or
// a first payment lands after the browser was closed).
func (s *Service) Reconcile(ctx context.Context, orgs []string) (synced int, errs int) {
	for _, org := range orgs {
		p := s.Plans.Resolve(ctx, org)
		if p.Provider != Provider || (p.ProviderSub == "" && p.ProviderPending == "") {
			continue
		}
		if err := s.Sync(ctx, org); err != nil {
			errs++
			s.log().Warn("billing sync failed", "org", org, "err", err)
			continue
		}
		synced++
	}
	return synced, errs
}
