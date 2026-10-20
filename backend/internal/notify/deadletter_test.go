package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// ---- default plan stamp (organizations without an org_plans row) ----

func TestPlanResetterStampsDefaultPlanOrgs(t *testing.T) {
	ps := NewMemPlans()
	ctx := context.Background()
	spent := map[string]float64{"o3": 3}
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	r := &PlanResetter{Plans: &Plans{Store: ps, DefaultFreeCapUSD: 5}, Orgs: orgList{"o3"}, Now: func() time.Time { return now },
		Spend: func(_ context.Context, org string) (float64, error) { return spent[org], nil },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil))}

	if rep := r.RunOnce(ctx); rep.Stamped != 1 || rep.Reset != 0 {
		t.Fatalf("first sweep %+v", rep)
	}
	p, ok, _ := ps.GetPlan(ctx, "o3")
	if !ok || p.Plan != policy.PlanFree || p.CapUSD != 5 || p.SpendBaseUSD != 0 || p.PeriodStart == nil || !p.PeriodStart.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("stamp must equal the default plan and give no allowance: %+v", p)
	}
	if rep := r.RunOnce(ctx); rep.Stamped != 0 || rep.Checked != 0 {
		t.Fatalf("idempotent within the month: %+v", rep)
	}
	now = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	spent["o3"] = 4.2
	if rep := r.RunOnce(ctx); rep.Reset != 1 {
		t.Fatalf("november reset %+v", rep)
	}
	if p, _, _ = ps.GetPlan(ctx, "o3"); p.SpendBaseUSD != 4.2 {
		t.Fatalf("base %+v", p)
	}
	// a racing replica or an admin row is never overwritten
	if done, _ := ps.StampDefaultPlan(ctx, "o3", 9, now); done {
		t.Fatal("existing row must be kept")
	}
	// no default plan configured: nothing to stamp
	r2 := &PlanResetter{Plans: &Plans{Store: NewMemPlans()}, Orgs: orgList{"x"}, Now: func() time.Time { return now },
		Spend: func(context.Context, string) (float64, error) { return 0, nil }}
	if rep := r2.RunOnce(ctx); rep.Stamped != 0 {
		t.Fatalf("no default plan: %+v", rep)
	}
}

// ---- dead letters ----

func TestDeadLettersListAndRetry(t *testing.T) {
	mo := NewMemOutbox()
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	add := func(org, id, status string, at time.Time) {
		_ = mo.Enqueue(ctx, OutboxItem{OrgID: org, ID: id, Channel: ChanEmail, Recipient: "a@x.io", Payload: []byte("secret"), Status: status, Attempts: 6, LastError: "boom", CreatedAt: at})
	}
	add("o1", "d1", OutboxDead, t0)
	add("o1", "d2", OutboxDead, t0.Add(time.Hour))
	add("o1", "s1", OutboxSent, t0)
	add("o1", "p1", OutboxPending, t0)
	add("o2", "d3", OutboxDead, t0)
	svc := &Service{Outbox: mo, Now: func() time.Time { return t0.Add(24 * time.Hour) }}

	list, err := svc.ListDeadLetters(ctx, "o1")
	if err != nil || len(list) != 2 || list[0].ID != "d2" {
		t.Fatalf("list %+v %v", list, err)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "secret") {
		t.Fatal("payload must not be exposed")
	}
	// another tenant's row, a sent row and an unknown id are all not found
	for _, id := range []string{"d3", "s1", "zz"} {
		if _, err := svc.RetryDeadLetter(ctx, "o1", id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	dl, err := svc.RetryDeadLetter(ctx, "o1", "d1")
	if err != nil || dl.Attempts != 6 || dl.Channel != ChanEmail {
		t.Fatalf("retry %+v %v", dl, err)
	}
	for _, r := range mo.Rows("o1") {
		if r.ID == "d1" && (r.Status != OutboxPending || r.Attempts != 0 || r.LastError != "" || !r.NextAttemptAt.Equal(t0.Add(24*time.Hour))) {
			t.Fatalf("requeued row %+v", r)
		}
	}
	if _, err := svc.RetryDeadLetter(ctx, "o1", "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("a second retry must not claim again")
	}
	if list, _ = svc.ListDeadLetters(ctx, "o1"); len(list) != 1 {
		t.Fatalf("after retry %+v", list)
	}
}

func TestDeadLetterHandlersAuditAndTenant(t *testing.T) {
	mo := NewMemOutbox()
	_ = mo.Enqueue(context.Background(), OutboxItem{OrgID: "o1", ID: "d1", Channel: ChanSlack, Status: OutboxDead, Attempts: 6, Payload: []byte("x")})
	var audited []string
	var code int
	a := API{Svc: &Service{Outbox: mo}, Org: func(*http.Request) string { return "o1" }, Actor: func(*http.Request) string { return "u" },
		Fail: func(w http.ResponseWriter, err error) {
			code = 500
			if errors.Is(err, domain.ErrNotFound) {
				code = 404
			}
		},
		Write:    func(w http.ResponseWriter, status int, v any) { code = status },
		URLParam: func(*http.Request, string) string { return "d1" },
		Audit:    func(_ *http.Request, action, _, id string, _ map[string]any) { audited = append(audited, action+":"+id) }}
	req := httptest.NewRequest("POST", "/", nil)
	a.RetryDeadLetter(httptest.NewRecorder(), req)
	if code != 200 || len(audited) != 1 || audited[0] != "notify.outbox_retry:d1" {
		t.Fatalf("code %d audit %v", code, audited)
	}
	a.RetryDeadLetter(httptest.NewRecorder(), req) // already pending: 404, no second audit
	if code != 404 || len(audited) != 1 {
		t.Fatalf("second: code %d audit %v", code, audited)
	}
	a.Org = func(*http.Request) string { return "o2" }
	a.RetryDeadLetter(httptest.NewRecorder(), req)
	if code != 404 {
		t.Fatalf("other tenant: %d", code)
	}
}
