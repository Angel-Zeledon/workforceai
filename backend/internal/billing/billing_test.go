package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/policy"
)

// fakeOnvo keeps customers and subscriptions in memory and answers the endpoints billing uses.
type fakeOnvo struct {
	mu        sync.Mutex
	subs      map[string]map[string]any
	customers int
	cancelled []string
	noDelete  bool // the API has no DELETE: cancelAt is used
}

func (f *fakeOnvo) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer onvo_test_secret_key_x" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"bad key"}`))
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/customers":
			f.customers++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cus_" + string(rune('0'+f.customers)), "name": in["name"]})
		case r.Method == "POST" && r.URL.Path == "/v1/subscriptions":
			n := "sub_" + string(rune('a'+len(f.subs)))
			items := in["items"].([]any)
			s := map[string]any{"id": n, "status": "incomplete", "customerId": in["customerId"], "metadata": in["metadata"],
				"items": []any{map[string]any{"price": map[string]any{"id": items[0].(map[string]any)["priceId"]}}}}
			f.subs[n] = s
			_ = json.NewEncoder(w).Encode(s)
		case strings.HasPrefix(r.URL.Path, "/v1/subscriptions/"):
			s, ok := f.subs[id]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			switch r.Method {
			case "GET":
			case "DELETE":
				if f.noDelete {
					w.WriteHeader(405)
					return
				}
				s["status"] = "canceled"
				f.cancelled = append(f.cancelled, id)
			case "POST":
				if v, ok := in["cancelAtPeriodEnd"]; ok {
					s["cancelAtPeriodEnd"] = v
				}
				if _, ok := in["cancelAt"]; ok {
					s["status"] = "canceled"
					f.cancelled = append(f.cancelled, id)
				}
			}
			_ = json.NewEncoder(w).Encode(s)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
}

func (f *fakeOnvo) set(id, status string) {
	f.mu.Lock()
	f.subs[id]["status"] = status
	f.mu.Unlock()
}

type env struct {
	svc    *Service
	fake   *fakeOnvo
	plans  *notify.Plans
	audits []map[string]any
}

const whsec = "webhook_secret_test"

var bg = context.Background()

func newEnv(t *testing.T) *env {
	t.Helper()
	f := &fakeOnvo{subs: map[string]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	plans := &notify.Plans{Store: notify.NewMemPlans(), DefaultFreeCapUSD: 1, CacheTTL: time.Nanosecond}
	e := &env{fake: f, plans: plans}
	e.svc = &Service{Onvo: &Onvo{Key: "onvo_test_secret_key_x", Base: srv.URL}, PublishableKey: "onvo_test_publishable_key_x",
		WebhookSecret: whsec, Plans: plans,
		Prices: map[string]string{"pro_monthly": "price_pm", "pro_yearly": "price_py", "max_monthly": "price_mm"},
		Audit:  func(_ context.Context, _, _ string, d map[string]any) { e.audits = append(e.audits, d) }}
	return e
}

func (e *env) plan(org string) notify.Plan { return e.plans.Resolve(bg, org) }

func TestBuyPro(t *testing.T) {
	e := newEnv(t)
	st, err := e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "owner@acme.test")
	if err != nil || st.SubscriptionID != "sub_a" || st.CustomerID != "cus_1" || st.PublishableKey == "" {
		t.Fatalf("start %+v %v", st, err)
	}
	if p := e.plan("org1"); p.Plan != policy.PlanFree || p.ProviderPending != "sub_a" || p.ProviderRef != "cus_1" {
		t.Fatalf("nothing changes before paying: %+v", p)
	}
	// The browser says "paid" too early: ONVO still says incomplete.
	if err := e.svc.Confirm(bg, "org1", "sub_a"); !errors.Is(err, ErrNotPaid) {
		t.Fatalf("unpaid confirm: %v", err)
	}
	e.fake.set("sub_a", "active")
	if err := e.svc.Confirm(bg, "org1", "sub_a"); err != nil {
		t.Fatal(err)
	}
	p := e.plan("org1")
	if p.Plan != policy.PlanPro || p.CapUSD != 12 || p.ProviderSub != "sub_a" || p.ProviderPending != "" {
		t.Fatalf("after paying: %+v", p)
	}
	if err := e.svc.Confirm(bg, "org1", "sub_a"); err != nil || len(e.audits) != 1 {
		t.Fatalf("confirming twice changes nothing: %v %d", err, len(e.audits))
	}
	if _, err := e.svc.Start(bg, "org1", policy.PlanPro, Yearly, "Acme", ""); !errors.Is(err, ErrSamePlan) {
		t.Fatalf("same plan: %v", err)
	}
	if e.fake.customers != 1 {
		t.Fatal("one customer per organization")
	}
}

func TestUpgradeCancelsTheOldSubscriptionFirst(t *testing.T) {
	e := newEnv(t)
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	e.fake.set("sub_a", "active")
	_ = e.svc.Confirm(bg, "org1", "sub_a")
	st, err := e.svc.Start(bg, "org1", policy.PlanMax, Monthly, "Acme", "")
	if err != nil || st.CustomerID != "cus_1" {
		t.Fatalf("upgrade start %+v %v", st, err)
	}
	if p := e.plan("org1"); p.Plan != policy.PlanPro || p.ProviderSub != "sub_a" || p.ProviderPending != "sub_b" {
		t.Fatalf("still Pro until Max is paid: %+v", p)
	}
	e.fake.set("sub_b", "active")
	if err := e.svc.Confirm(bg, "org1", "sub_b"); err != nil {
		t.Fatal(err)
	}
	if p := e.plan("org1"); p.Plan != policy.PlanMax || p.CapUSD != 45 || p.ProviderSub != "sub_b" {
		t.Fatalf("max: %+v", p)
	}
	if len(e.fake.cancelled) != 1 || e.fake.cancelled[0] != "sub_a" {
		t.Fatalf("Pro must be cancelled: %v", e.fake.cancelled)
	}
}

func TestOldSubscriptionCancelledWithCancelAtWhenThereIsNoDelete(t *testing.T) {
	e := newEnv(t)
	e.fake.noDelete = true
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	e.fake.set("sub_a", "active")
	_ = e.svc.Confirm(bg, "org1", "sub_a")
	_, _ = e.svc.Start(bg, "org1", policy.PlanMax, Monthly, "Acme", "")
	e.fake.set("sub_b", "active")
	if err := e.svc.Confirm(bg, "org1", "sub_b"); err != nil || len(e.fake.cancelled) != 1 {
		t.Fatalf("%v %v", err, e.fake.cancelled)
	}
}

func TestBrowserClosedBeforeConfirmSyncFinishesIt(t *testing.T) {
	e := newEnv(t)
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	if n, errs := e.svc.Reconcile(bg, []string{"org1", "org2"}); n != 1 || errs != 0 || e.plan("org1").Plan != policy.PlanFree {
		t.Fatalf("not paid yet: %d %d", n, errs)
	}
	e.fake.set("sub_a", "active") // paid; the person closed the tab
	e.svc.Reconcile(bg, []string{"org1"})
	if p := e.plan("org1"); p.Plan != policy.PlanPro || p.ProviderSub != "sub_a" {
		t.Fatalf("reconcile confirms: %+v", p)
	}
}

func TestCancelAtPeriodEndThenFree(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Cancel(bg, "org1", false); !errors.Is(err, ErrNoSubscription) {
		t.Fatal(err)
	}
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	e.fake.set("sub_a", "active")
	_ = e.svc.Confirm(bg, "org1", "sub_a")
	if err := e.svc.Cancel(bg, "org1", false); err != nil {
		t.Fatal(err)
	}
	if st := e.svc.StatusOf(bg, "org1"); !st.Subscribed || !st.CancelAtPeriodEnd {
		t.Fatalf("status %+v", st)
	}
	_ = e.svc.Cancel(bg, "org1", true) // changed their mind
	if st := e.svc.StatusOf(bg, "org1"); st.CancelAtPeriodEnd {
		t.Fatal("resume")
	}
	e.svc.Reconcile(bg, []string{"org1"})
	if e.plan("org1").Plan != policy.PlanPro {
		t.Fatal("still paid: still Pro")
	}
	e.fake.set("sub_a", "canceled") // the period ended
	e.svc.Reconcile(bg, []string{"org1"})
	p := e.plan("org1")
	if p.Plan != policy.PlanFree || p.CapUSD != 1 || p.ProviderSub != "" || p.ProviderRef != "cus_1" {
		t.Fatalf("free again, customer kept: %+v", p)
	}
}

func TestFailedPaymentsKeepThePlanUntilOnvoGivesUp(t *testing.T) {
	e := newEnv(t)
	_, _ = e.svc.Start(bg, "org1", policy.PlanMax, Monthly, "Acme", "")
	e.fake.set("sub_a", "active")
	_ = e.svc.Confirm(bg, "org1", "sub_a")
	webhook := func() error {
		body := []byte(`{"type":"subscription.renewal.failed","data":{"subscriptionId":"sub_a","subscriptionStatus":"active"}}`)
		return e.svc.HandleWebhook(bg, body, whsec)
	}
	e.fake.set("sub_a", "past_due")
	if err := webhook(); err != nil || e.plan("org1").Plan != policy.PlanMax {
		t.Fatalf("past_due keeps Max: %v", err)
	}
	if !e.svc.StatusOf(bg, "org1").PastDue {
		t.Fatal("the screen must say the payment failed")
	}
	// Another card for the same plan: allowed only while the payment is failing; it replaces the failing subscription.
	st, err := e.svc.Start(bg, "org1", policy.PlanMax, Monthly, "Acme", "")
	if err != nil {
		t.Fatalf("pay Max again with another card: %v", err)
	}
	e.fake.set(st.SubscriptionID, "active")
	if err := e.svc.Confirm(bg, "org1", st.SubscriptionID); err != nil {
		t.Fatal(err)
	}
	if p := e.plan("org1"); p.Plan != policy.PlanMax || p.ProviderSub != st.SubscriptionID || len(e.fake.cancelled) != 1 || e.fake.cancelled[0] != "sub_a" {
		t.Fatalf("new card: %+v %v", p, e.fake.cancelled)
	}
	if _, err := e.svc.Start(bg, "org1", policy.PlanMax, Monthly, "Acme", ""); !errors.Is(err, ErrSamePlan) {
		t.Fatalf("paid again: same plan: %v", err)
	}
	e.fake.set(st.SubscriptionID, "unpaid")
	webhook = func() error {
		body := []byte(`{"type":"subscription.renewal.failed","data":{"subscriptionId":"` + st.SubscriptionID + `"}}`)
		return e.svc.HandleWebhook(bg, body, whsec)
	}
	if err := webhook(); err != nil || e.plan("org1").Plan != policy.PlanFree {
		t.Fatalf("unpaid: %v %+v", err, e.plan("org1"))
	}
}

func TestWebhookTrustsOnlyOnvo(t *testing.T) {
	e := newEnv(t)
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	body := []byte(`{"type":"subscription.renewal.succeeded","data":{"subscriptionId":"sub_a"}}`)
	if err := e.svc.HandleWebhook(bg, body, "webhook_secret_wrong"); !errors.Is(err, ErrWebhookSecret) {
		t.Fatal(err)
	}
	// A real event with a forged story: the subscription is read from ONVO, which says it is not paid.
	if err := e.svc.HandleWebhook(bg, []byte(`{"type":"subscription.renewal.succeeded","data":{"subscriptionId":"sub_a","status":"paid"}}`), whsec); err != nil {
		t.Fatal(err)
	}
	if e.plan("org1").Plan != policy.PlanFree {
		t.Fatal("an event must not grant a plan on its own")
	}
	// Events without a subscription, unknown subscriptions, and other apps' subscriptions are ignored.
	for _, b := range []string{`{"type":"payment-intent.succeeded","data":{"id":"pi_1"}}`, `{"type":"subscription.renewal.succeeded","data":{"subscriptionId":"sub_zz"}}`, `not json`} {
		if err := e.svc.HandleWebhook(bg, []byte(b), whsec); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
	}
	e.fake.subs["sub_other"] = map[string]any{"id": "sub_other", "status": "active", "metadata": map[string]any{"app": "stockai", "org": "org1"}}
	_ = e.svc.HandleWebhook(bg, []byte(`{"type":"subscription.renewal.succeeded","data":{"subscriptionId":"sub_other"}}`), whsec)
	if e.plan("org1").Plan != policy.PlanFree {
		t.Fatal("another app's subscription")
	}
	// Paid: the webhook confirms it like the browser would.
	e.fake.set("sub_a", "active")
	if err := e.svc.HandleWebhook(bg, body, whsec); err != nil || e.plan("org1").Plan != policy.PlanPro {
		t.Fatalf("%v %+v", err, e.plan("org1"))
	}
}

func TestConfirmOnlyItsOwnSubscription(t *testing.T) {
	e := newEnv(t)
	_, _ = e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", "")
	_, _ = e.svc.Start(bg, "org2", policy.PlanMax, Monthly, "Other", "")
	e.fake.set("sub_b", "active")
	if err := e.svc.Confirm(bg, "org1", "sub_b"); !errors.Is(err, ErrForeign) {
		t.Fatalf("org1 confirming org2's subscription: %v", err)
	}
	if e.plan("org1").Plan != policy.PlanFree {
		t.Fatal("foreign")
	}
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{policy.PlanFree, Monthly}, {"gold", Monthly}, {policy.PlanPro, "weekly"}, {policy.PlanMax, Yearly}} {
		if _, err := e.svc.Start(bg, "org1", c[0], c[1], "Acme", ""); !errors.Is(err, ErrBadPlan) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	if got := strings.Join(e.svc.Available(), ","); got != "pro_monthly,pro_yearly,max_monthly" {
		t.Fatalf("available %s", got)
	}
	e.svc.Onvo.Key = "onvo_test_secret_key_wrong"
	var apiErr *APIError
	if _, err := e.svc.Start(bg, "org1", policy.PlanPro, Monthly, "Acme", ""); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("onvo error: %v", err)
	}
}

func TestSubscriptionShapes(t *testing.T) {
	for _, raw := range []string{
		`{"id":"s","status":"active","customerId":"c","items":[{"priceId":"p"}],"currentPeriodEnd":"2026-12-01T00:00:00Z"}`,
		`{"id":"s","status":"active","customer":{"id":"c"},"items":[{"price":{"id":"p"}}],"periodEnd":"2026-12-01T00:00:00Z"}`,
		`{"id":"s","status":"active","customer":"c","items":[{"priceId":"p"}],"periodEnd":"2026-12-01T00:00:00Z","metadata":{"n":1,"app":"workforceai"}}`,
	} {
		var s Subscription
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Customer != "c" || len(s.PriceIDs) != 1 || s.PriceIDs[0] != "p" || s.PeriodEnd == nil {
			t.Fatalf("%s -> %+v %v", raw, s, err)
		}
	}
}
