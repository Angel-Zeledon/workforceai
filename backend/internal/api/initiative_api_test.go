package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

func (e *env) raiseInitiative(agent, key string) application.Initiative {
	e.t.Helper()
	out, err := e.init.Raise(context.Background(), application.RaiseInput{AgentID: agent, Signal: "margin_drop", SignalKey: key,
		Title: "El margen bajó 3 %", Body: "¿Lo reviso?", Prompt: "Revisa el margen " + key})
	if err != nil || !out.Raised {
		e.t.Fatalf("raise = %+v %v", out, err)
	}
	return out.Initiative
}

func TestInitiativeAPIListAcceptDismiss(t *testing.T) {
	e := newEnv(t, opts{initiative: true})
	a, b := e.raiseInitiative("sales", "a"), e.raiseInitiative("sales", "b")

	resp, body := e.do("GET", "/api/v1/initiatives?status=pending", "", nil, nil)
	var list []application.Initiative
	_ = json.Unmarshal(body, &list)
	if resp.StatusCode != 200 || len(list) != 2 {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	if c := e.status("GET", "/api/v1/initiatives?status=bogus", "", nil); c != 400 {
		t.Errorf("bogus status = %d", c)
	}

	resp, body = e.do("POST", "/api/v1/initiatives/"+a.ID+"/accept", "", nil, nil)
	var acc application.Initiative
	_ = json.Unmarshal(body, &acc)
	if resp.StatusCode != http.StatusAccepted || acc.Status != application.InitiativeAccepted || acc.RequestID == "" {
		t.Fatalf("accept = %d %s", resp.StatusCode, body)
	}
	// The request exists through the normal path.
	if c := e.status("GET", "/api/v1/requests/"+acc.RequestID, "", nil); c != 200 {
		t.Errorf("request %s = %d", acc.RequestID, c)
	}
	if c := e.status("POST", "/api/v1/initiatives/"+a.ID+"/accept", "", nil); c != 409 {
		t.Errorf("second accept = %d", c)
	}

	resp, body = e.do("POST", "/api/v1/initiatives/"+b.ID+"/dismiss", "", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dismiss = %d %s", resp.StatusCode, body)
	}
	if c := e.status("POST", "/api/v1/initiatives/missing/dismiss", "", nil); c != 404 {
		t.Errorf("dismiss missing = %d", c)
	}
	_, body = e.do("GET", "/api/v1/initiatives?status=pending", "", nil, nil)
	_ = json.Unmarshal(body, &list)
	if len(list) != 0 {
		t.Errorf("pending after resolving both = %s", body)
	}
}

func TestInitiativeAPISettings(t *testing.T) {
	e := newEnv(t, opts{initiative: true})
	resp, body := e.do("GET", "/api/v1/settings/initiative", "", nil, nil)
	var s application.InitiativeSettings
	_ = json.Unmarshal(body, &s)
	if resp.StatusCode != 200 || !s.Enabled || s.DailyLimit != application.DefaultInitiativeDaily {
		t.Fatalf("defaults = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("PUT", "/api/v1/settings/initiative", "", application.InitiativeSettings{Enabled: true, DailyLimit: 1, MonthlyLimit: 5, AgentDaily: map[string]int{"sales": 0}}, nil)
	_ = json.Unmarshal(body, &s)
	if resp.StatusCode != 200 || s.DailyLimit != 1 || s.AgentDaily["sales"] != 0 {
		t.Fatalf("put = %d %s", resp.StatusCode, body)
	}
	if c := e.status("PUT", "/api/v1/settings/initiative", "", application.InitiativeSettings{DailyLimit: -3}); c != 400 {
		t.Errorf("invalid put = %d", c)
	}
	out, _ := e.init.Raise(context.Background(), application.RaiseInput{AgentID: "sales", Signal: "s", Title: "t", Prompt: "p"})
	if out.Raised || out.Reason != "agent_limit" {
		t.Errorf("muted agent: %+v", out)
	}
}

func TestInitiativeAPIPermissionsAndTenancy(t *testing.T) {
	e := newEnv(t, opts{auth: true, initiative: true})
	owner := e.register("owner@example.com", "Acme")
	other := e.register("other@example.com", "Other")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	member := e.member(owner, "member@example.com", auth.RoleMember)

	out, err := e.init.Raise(application.WithOrg(context.Background(), owner.OrgID), application.RaiseInput{AgentID: "sales", Signal: "s", SignalKey: "k", Title: "t", Prompt: "p"})
	if err != nil || !out.Raised {
		t.Fatalf("raise = %+v %v", out, err)
	}
	id := out.Initiative.ID

	if c := e.status("GET", "/api/v1/initiatives", "", nil); c != 401 {
		t.Errorf("anonymous list = %d", c)
	}
	if c := e.status("GET", "/api/v1/initiatives", viewer, nil); c != 200 {
		t.Errorf("viewer list = %d", c)
	}
	if c := e.status("POST", "/api/v1/initiatives/"+id+"/accept", viewer, nil); c != 403 {
		t.Errorf("viewer accept = %d, want 403", c)
	}
	if c := e.status("PUT", "/api/v1/settings/initiative", member, application.InitiativeSettings{Enabled: true, DailyLimit: 1, MonthlyLimit: 1}); c != 403 {
		t.Errorf("member put settings = %d, want 403", c)
	}
	// Another organization neither sees nor resolves it.
	resp, body := e.do("GET", "/api/v1/initiatives", other.AccessToken, nil, nil)
	var list []application.Initiative
	_ = json.Unmarshal(body, &list)
	if resp.StatusCode != 200 || len(list) != 0 {
		t.Errorf("other org list = %d %s", resp.StatusCode, body)
	}
	if c := e.status("POST", "/api/v1/initiatives/"+id+"/dismiss", other.AccessToken, nil); c != 404 {
		t.Errorf("other org dismiss = %d, want 404", c)
	}
	if c := e.status("POST", "/api/v1/initiatives/"+id+"/dismiss", member, nil); c != 200 {
		t.Errorf("member dismiss = %d", c)
	}
}

// Metric signals (migration 800): GET always carries them, a PUT may send only what changes (merged), an old
// client that omits them keeps them, bad thresholds are 400.
func TestInitiativeAPISignalSettings(t *testing.T) {
	e := newEnv(t, opts{initiative: true})
	var s application.InitiativeSettings
	_, body := e.do("GET", "/api/v1/settings/initiative", "", nil, nil)
	_ = json.Unmarshal(body, &s)
	if s.Signals == nil || !s.Signals.MetricChange.Enabled || s.Signals.SpendPace.PacePct != application.DefaultSpendPacePct {
		t.Fatalf("default signals = %s", body)
	}
	resp, body := e.do("PUT", "/api/v1/settings/initiative", "",
		[]byte(`{"enabled":true,"daily_limit":3,"monthly_limit":30,"signals":{"spend_pace":{"enabled":false},"quality_drop":{"drop_points":15}}}`), nil)
	s = application.InitiativeSettings{}
	_ = json.Unmarshal(body, &s)
	if resp.StatusCode != 200 || s.Signals.SpendPace.Enabled || s.Signals.SpendPace.PacePct != application.DefaultSpendPacePct ||
		s.Signals.QualityDrop.DropPoints != 15 || !s.Signals.QualityDrop.Enabled || !s.Signals.MetricChange.Enabled {
		t.Fatalf("partial signals put = %d %s", resp.StatusCode, body)
	}
	// An older client (no "signals") keeps them.
	resp, body = e.do("PUT", "/api/v1/settings/initiative", "", []byte(`{"enabled":true,"daily_limit":2,"monthly_limit":20}`), nil)
	s = application.InitiativeSettings{}
	_ = json.Unmarshal(body, &s)
	if resp.StatusCode != 200 || s.DailyLimit != 2 || s.Signals.SpendPace.Enabled || s.Signals.QualityDrop.DropPoints != 15 {
		t.Fatalf("put without signals = %d %s", resp.StatusCode, body)
	}
	for _, bad := range []string{
		`{"enabled":true,"daily_limit":3,"monthly_limit":30,"signals":{"metric_change":{"change_pct":0}}}`,
		`{"enabled":true,"daily_limit":3,"monthly_limit":30,"signals":{"approval_aging":{"min_age_hours":5000}}}`,
		`{"enabled":true,"daily_limit":3,"monthly_limit":30,"signals":{"spend_pace":{"min_days":40}}}`,
	} {
		if c := e.status("PUT", "/api/v1/settings/initiative", "", []byte(bad)); c != 400 {
			t.Errorf("%s = %d, want 400", bad, c)
		}
	}
}

// POST /initiatives/scan evaluates the signals now; only org:manage may call it.
func TestInitiativeAPIScan(t *testing.T) {
	e := newEnv(t, opts{auth: true, initiative: true})
	owner := e.register("owner@example.com", "Acme")
	member := e.member(owner, "member@example.com", auth.RoleMember)
	old := time.Now().Add(-30 * time.Hour)
	if err := e.store.CreateApproval(context.Background(), owner.OrgID, domain.Approval{ID: "ap-old", AgentID: "sales", Title: "Enviar propuesta",
		Risk: "high", Status: domain.ApprovalPending, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if c := e.status("POST", "/api/v1/initiatives/scan", member, nil); c != 403 {
		t.Errorf("member scan = %d, want 403", c)
	}
	resp, body := e.do("POST", "/api/v1/initiatives/scan", owner.AccessToken, nil, nil)
	var out struct {
		Raised  int                      `json:"raised"`
		Results []application.ScanResult `json:"results"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != 200 || out.Raised != 1 || len(out.Results) != 1 || out.Results[0].Signal != application.SignalApprovalAging ||
		out.Results[0].AgentID != "assistant" {
		t.Fatalf("scan = %d %s", resp.StatusCode, body)
	}
	// The same oldest approval is not suggested twice inside the window.
	_, body = e.do("POST", "/api/v1/initiatives/scan", owner.AccessToken, nil, nil)
	out.Raised, out.Results = 0, nil
	_ = json.Unmarshal(body, &out)
	if out.Raised != 0 || len(out.Results) != 1 || out.Results[0].Reason != "duplicate" {
		t.Fatalf("second scan = %s", body)
	}
}
