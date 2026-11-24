package api_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	longmem "aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/officeaudience"
)

// officesWorldSealed is officesWorld with the live-event rule of D3 installed on the recorder, as the server wires it.
func officesWorldSealed(t *testing.T, more ...func(*api.Deps)) (e *env, owner, ana, marta, luis, ventas, soporte, org string) {
	t.Helper()
	return officesWorldSealedCfg(t, nil, more...)
}

// officesWorldSealedCfg is officesWorldSealed with a last word on the orchestrator configuration.
func officesWorldSealedCfg(t *testing.T, cfg func(*application.Config), more ...func(*api.Deps)) (e *env, owner, ana, marta, luis, ventas, soporte, org string) {
	t.Helper()
	var d *api.Deps
	e = newEnv(t, opts{auth: true, orgchart: true, away: true, code: true, v2: true, quality: true, ws: true, trust: true, cfg: cfg, deps: func(x *api.Deps) {
		withOfficesMulti(x)
		docs := longmem.NewMemStore()
		x.Memory = longmem.New(longmem.Config{Docs: docs, Retriever: &longmem.FTS{Docs: docs}})
		x.Commands = &api.CommandsDeps{}
		for _, f := range more {
			f(x)
		}
		d = x
	}})
	o := e.register("owner-ao@example.com", "OwnerAO")
	owner, org = o.AccessToken, o.OrgID
	ana = e.member(o, "ana-ao@example.com", auth.RoleMember)
	marta = e.member(o, "marta-ao@example.com", auth.RoleMember)
	luis = e.member(o, "luis-ao@example.com", auth.RoleMember)
	anaID, martaID := e.userID(ana), e.userID(marta)
	v := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Ventas", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	ventas = v["id"].(string)
	s := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Soporte", "head": map[string]any{"kind": "person", "id": martaID}}, 201)
	soporte = s["id"].(string)
	e.json("PUT", api1+"/agents/sales/job", owner, map[string]any{"department_id": ventas}, 200)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": ventas}, 201)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": soporte}, 201)
	d.Rec.SetAudienceFor(officeaudience.New(d.Offices, e.store, func(c context.Context, org, id string) string {
		return d.Orch.RequestedBy(application.WithOrg(c, org), id)
	}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return
}

// A request asked from a private office is that office's work whatever agent did it (the assistant, a General agent):
// its text, its tasks, report, approvals, activity, away summary, needs-you and files never reach who is not in the
// office (an admin who is not a member included), and a member of the office reads all of it.
func TestOfficeLeakWorkSurfacesAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte, _ := officesWorldSealed(t)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	// the member asks for work from the private office; a General agent (the assistant) takes it
	r := e.json("POST", api1+"/requests", marta, map[string]any{"text": "SECRETO-pedido redacta el informe de soporte", "office_id": soporte}, 202)
	id, _ := r["request_id"].(string)
	if id == "" {
		t.Fatalf("request = %v", r)
	}
	e.eventually("the request finishes", func() bool {
		d := e.json("GET", api1+"/requests/"+id, marta, nil, 200)
		s, _ := d["status"].(string)
		return s == "done" || s == "failed" || s == "awaiting_approval"
	})
	time.Sleep(300 * time.Millisecond)
	// an approval waits for a task of that request (the agent is a General one: only the request's office tells)
	var taskID string
	for _, tk := range e.list(api1+"/tasks", marta) {
		if tk["request_id"] == id {
			taskID, _ = tk["id"].(string)
		}
	}
	if taskID == "" {
		t.Fatalf("no task of the request")
	}
	ap, _, err := e.appr.Request(context.Background(), domain.Approval{TaskID: taskID, AgentID: "assistant", Action: "email.send", Risk: "high", Title: "Enviar el informe de soporte"})
	if err != nil {
		t.Fatal(err)
	}

	const secret = "SECRETO-pedido"
	paths := []string{"/requests", "/requests/" + id, "/requests/" + id + "/audit", "/tasks", "/reports", "/approvals", "/approvals/inbox",
		"/activity", "/needs-you", "/needs-you/count", "/away-summary?min_hours=0", "/conversations", "/conversations?kind=request",
		"/chats/threads", "/artifacts", "/projects", "/questions", "/debates", "/cost", "/agents/assistant/tasks"}
	for _, who := range []struct{ name, token string }{{"admin not in the office", owner}, {"member of another office", ana}, {"nobody's", luis}} {
		for _, p := range paths {
			resp, raw := e.do("GET", api1+p, who.token, nil, nil)
			if resp.StatusCode == 200 && strings.Contains(string(raw), secret) || resp.StatusCode == 200 && (strings.Contains(string(raw), id) || strings.Contains(string(raw), ap.ID)) { // its text, or anything of it by its id
				t.Fatalf("%s: GET %s leaks the private office's request: %.600s", who.name, p, raw)
			}
		}
	}
	// ...and the member of the office does see it (the test would pass for nothing otherwise)
	if _, raw := e.do("GET", api1+"/requests/"+id, marta, nil, nil); !strings.Contains(string(raw), secret) {
		t.Fatalf("the member does not see the request: %s", raw)
	}
	for _, p := range []string{"/tasks", "/reports", "/approvals"} { // the work itself is there for the member (the check above is not empty)
		if _, raw := e.do("GET", api1+p, marta, nil, nil); !strings.Contains(string(raw), id) && !strings.Contains(string(raw), ap.ID) {
			t.Fatalf("the member does not see %s of the request: %.300s", p, raw)
		}
	}
}
