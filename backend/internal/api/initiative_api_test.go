package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
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
