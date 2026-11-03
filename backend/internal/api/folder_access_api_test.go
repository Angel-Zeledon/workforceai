package api_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/orgchart"
)

// Folder access per agent over HTTP: everyone reads it, admins and the head of the agent's team change it.
func TestFolderAccessAuthorization(t *testing.T) {
	var leadID string // the org chart knows the lead as a person of the organization
	e := newEnv(t, opts{auth: true, drive: true, orgchart: true, deps: func(d *api.Deps) {
		d.OrgChart.People = func(context.Context, string) ([]orgchart.Person, error) {
			return []orgchart.Person{{UserID: leadID, Name: "Lead", Role: "member"}}, nil
		}
	}})
	owner := e.register("owner-fa@example.com", "OwnerFA")
	lead := e.member(owner, "lead-fa@example.com", auth.RoleMember)
	member := e.member(owner, "member-fa@example.com", auth.RoleMember)
	rival := e.register("rival-fa@example.com", "RivalFA")
	me := e.json("GET", api1+"/auth/me", lead, nil, 200)
	leadID = me["user"].(map[string]any)["id"].(string)

	fa := api1 + "/agents/sales/folder-access"
	got := e.json("GET", fa, member, nil, 200)
	if got["can_manage"] != false || !strings.Contains(mustJSON(t, got["entries"]), `"/Agents/sales"`) {
		t.Fatalf("read: %v", got)
	}
	body := map[string]any{"path": "/Clients/Acme", "level": "edit"}
	e.json("PUT", fa, member, body, 403)
	e.json("PUT", fa, lead, body, 403) // not yet the head of the agent's team

	// the lead heads the Sales team, where the agent works: now they may change it
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Sales", "head": map[string]any{"kind": "person", "id": leadID}}, 201)
	e.json("PUT", api1+"/agents/sales/job", owner.AccessToken, map[string]any{"department_id": dep["id"]}, 200)
	if got = e.json("GET", fa, lead, nil, 200); got["can_manage"] != true {
		t.Fatalf("lead can manage: %v", got)
	}
	got = e.json("PUT", fa, lead, body, 200)
	if !strings.Contains(mustJSON(t, got["entries"]), `"level":"edit","path":"/Clients/Acme","source":"manual"`) {
		t.Fatalf("after put: %v", got)
	}
	// the lead heads Sales, not Accounting
	e.json("PUT", api1+"/agents/accounting/folder-access", lead, body, 403)
	// admins (the owner) may change any agent; bad input is refused
	e.json("PUT", api1+"/agents/accounting/folder-access", owner.AccessToken, map[string]any{"path": "/Shared", "level": "none"}, 200)
	e.json("PUT", fa, owner.AccessToken, map[string]any{"path": "/Agents/sales", "level": "none"}, 400)
	e.json("PUT", fa, owner.AccessToken, map[string]any{"path": "/Clients", "level": "owner"}, 400)
	e.json("PUT", api1+"/agents/ghost/folder-access", owner.AccessToken, body, 404)

	// "Qué agentes la ven": per folder, the level of each agent
	w := e.json("GET", api1+"/drive/folder-access?path="+url.QueryEscape("/Clients/Acme/Contratos"), member, nil, 200)
	js := mustJSON(t, w["agents"])
	if !strings.Contains(js, `"agent_id":"sales","can_manage":false,"level":"edit","name"`) || !strings.Contains(js, `"source":"manual"`) {
		t.Fatalf("folder agents: %s", js)
	}
	sh := mustJSON(t, e.json("GET", api1+"/drive/folder-access?path=/Shared", member, nil, 200)["agents"])
	if !strings.Contains(sh, `"agent_id":"accounting","can_manage":false,"level":"none"`) || strings.Count(sh, `"level":"read"`) < 2 {
		t.Fatalf("shared defaults: %s", sh)
	}
	e.json("GET", api1+"/drive/folder-access?path=/", member, nil, 400)
	// other organizations never see it
	if r := e.json("GET", fa, rival.AccessToken, nil, 200); strings.Contains(mustJSON(t, r), "Acme") {
		t.Fatalf("leaked across organizations: %v", r)
	}
}
