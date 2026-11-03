package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/billing"
)

// An ONVO with one customer and one subscription that the test marks as paid.
type onvoAPI struct {
	mu     sync.Mutex
	status string
	org    string
}

func (o *onvoAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()
		sub := map[string]any{"id": "sub_1", "status": o.status, "customerId": "cus_1",
			"metadata": map[string]string{"app": "workforceai", "org": o.org, "plan": "pro"}, "items": []map[string]string{{"priceId": "price_pm"}}}
		switch {
		case r.URL.Path == "/v1/customers":
			_, _ = w.Write([]byte(`{"id":"cus_1"}`))
		case r.URL.Path == "/v1/subscriptions":
			_ = json.NewEncoder(w).Encode(sub)
		case strings.HasPrefix(r.URL.Path, "/v1/subscriptions/sub_1"):
			if r.Method == "POST" {
				sub["cancelAtPeriodEnd"] = true
			}
			_ = json.NewEncoder(w).Encode(sub)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOnvoSubscribeConfirmCancelAndWebhook(t *testing.T) {
	org := application.DefaultConfig().OrgID
	o := &onvoAPI{status: "incomplete", org: org}
	srv := o.server(t)
	e := planEnv(t, true, func(d *api.Deps) {
		d.Billing = &billing.Service{Onvo: &billing.Onvo{Key: "onvo_test_secret_key_x", Base: srv.URL}, PublishableKey: "onvo_test_publishable_key_x",
			WebhookSecret: "webhook_secret_x", Prices: map[string]string{"pro_monthly": "price_pm", "max_monthly": "price_mm"}, Plans: d.Notify.Plans}
	})
	type view struct {
		Plan         string   `json:"plan"`
		Billing      string   `json:"billing"`
		SelfServe    bool     `json:"self_serve"`
		Available    []string `json:"available"`
		Subscription struct {
			Subscribed        bool `json:"subscribed"`
			CancelAtPeriodEnd bool `json:"cancel_at_period_end"`
		} `json:"subscription"`
	}
	get := func() view {
		_, raw := e.do("GET", "/api/v1/plan", "", nil, nil)
		var v view
		_ = json.Unmarshal(raw, &v)
		return v
	}
	if v := get(); v.Billing != "onvo" || v.SelfServe || v.Plan != "free" || strings.Join(v.Available, ",") != "pro_monthly,max_monthly" {
		t.Fatalf("view %+v", v)
	}
	if resp, raw := e.do("PUT", "/api/v1/plan", "", map[string]string{"plan": "max"}, nil); resp.StatusCode != 403 {
		t.Fatalf("PUT /plan with ONVO: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("POST", "/api/v1/plan/subscribe", "", map[string]string{"plan": "pro", "period": "yearly"}, nil); resp.StatusCode != 400 {
		t.Fatalf("no yearly price: %d %s", resp.StatusCode, raw)
	}
	resp, raw := e.do("POST", "/api/v1/plan/subscribe", "", map[string]string{"plan": "pro", "period": "monthly", "email": "o@acme.test"}, nil)
	if st := decodeInto[map[string]string](t, raw); resp.StatusCode != 200 || st["subscription_id"] != "sub_1" || st["publishable_key"] != "onvo_test_publishable_key_x" {
		t.Fatalf("subscribe %d %s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("POST", "/api/v1/plan/confirm", "", map[string]string{"subscription_id": "sub_1"}, nil); resp.StatusCode != 409 ||
		decodeInto[map[string]string](t, raw)["code"] != "not_paid" {
		t.Fatalf("confirm before paying: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := e.do("POST", "/api/v1/plan/confirm", "", map[string]string{"subscription_id": "sub_other"}, nil); resp.StatusCode != 403 {
		t.Fatalf("someone else's subscription: %d", resp.StatusCode)
	}
	o.mu.Lock()
	o.status = "active"
	o.mu.Unlock()
	if resp, raw := e.do("POST", "/api/v1/plan/confirm", "", map[string]string{"subscription_id": "sub_1"}, nil); resp.StatusCode != 200 {
		t.Fatalf("confirm %d %s", resp.StatusCode, raw)
	}
	if v := get(); v.Plan != "pro" || !v.Subscription.Subscribed {
		t.Fatalf("after paying %+v", v)
	}
	if resp, raw := e.do("POST", "/api/v1/plan/cancel", "", map[string]bool{"resume": false}, nil); resp.StatusCode != 200 {
		t.Fatalf("cancel %d %s", resp.StatusCode, raw)
	}

	ev := []byte(`{"type":"subscription.renewal.succeeded","data":{"subscriptionId":"sub_1"}}`)
	if resp, _ := e.do("POST", "/api/v1/billing/onvo/webhook", "", ev, nil); resp.StatusCode != 401 {
		t.Fatalf("webhook without secret: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/api/v1/billing/onvo/webhook", "", ev, map[string]string{"X-Webhook-Secret": "webhook_secret_x"}); resp.StatusCode != 200 {
		t.Fatalf("webhook: %d", resp.StatusCode)
	}
	// The period ended: ONVO cancelled it; the next event (or the job) moves the organization to Free.
	o.mu.Lock()
	o.status = "canceled"
	o.mu.Unlock()
	_, _ = e.do("POST", "/api/v1/billing/onvo/webhook", "", ev, map[string]string{"X-Webhook-Secret": "webhook_secret_x"})
	if v := get(); v.Plan != "free" || v.Subscription.Subscribed {
		t.Fatalf("after the period %+v", v)
	}
}
