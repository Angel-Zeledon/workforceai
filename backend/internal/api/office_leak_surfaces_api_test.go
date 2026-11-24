package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/sandbox"
)

// The surfaces that used to filter by agent only (outbox, code runs, thought audit, stalled work, rehearsals): the
// assistant (a General agent) works for a private office, so what it did there carries the office of its request or
// task and must follow it. The world has work asked from the private office (SECRETO) and from General (ABIERTO), both
// done by the assistant; chief is an admin who is a member of the office, owner an admin who is not.
type leakWorld struct {
	e                       *env
	owner, ana, marta, luis string
	chief                   string // an admin member of the private office
	soporte, org            string
	gw                      *gateway.Gateway
}

func newLeakWorld(t *testing.T) *leakWorld {
	t.Helper()
	w := &leakWorld{}
	w.e, w.owner, w.ana, w.marta, w.luis, _, w.soporte, w.org = officesWorldSealed(t, func(o *opts) { o.rehearsal = true }, withGateway(&w.gw), func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			is := &application.InitiativeService{Store: d.Store.(*orgSpy).Store.(application.InitiativeStore), Submit: d.Orch, Cfg: d.Cfg}
			d.Initiative = is
			d.Watchdog = &application.Watchdog{Orch: d.Orch, Initiative: is, Cfg: d.Cfg, Now: func() time.Time { return time.Now().Add(3 * time.Hour) }}
			d.Watchdog.Install()
		}
	})
	e := w.e
	e.json("PUT", api1+"/offices/"+w.soporte, w.owner, map[string]any{"visibility": "private"}, 200)
	boss := e.member(&auth.Session{OrgID: w.org, User: auth.UserView{ID: e.userID(w.owner)}}, "chief-fugas@example.com", auth.RoleAdmin)
	e.json("PUT", api1+"/offices/"+w.soporte+"/members/"+e.userID(boss), w.marta, map[string]any{"role": "member"}, 200)
	w.chief = boss
	ctx, now, st := context.Background(), time.Now().UTC(), e.store
	for _, x := range []struct{ tag, office string }{{"SECRETO", w.soporte}, {"ABIERTO", ""}} {
		req := "req-" + x.tag
		if err := st.CreateRequest(ctx, w.org, domain.Request{ID: req, Text: x.tag + "-pedido", Status: domain.RequestRunning, CreatedAt: now, OfficeID: x.office}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateTask(ctx, w.org, domain.Task{ID: "task-" + x.tag, RequestID: req, Title: x.tag + "-tarea", AgentID: "assistant", OfficeID: x.office,
			Status: domain.TaskRunning, DependsOn: []string{}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// sees reports whether GET path answers 200 with the marker in the body (404/403 and a body without it count as no).
func (w *leakWorld) sees(token, method, path, marker string, body any) bool {
	w.e.t.Helper()
	resp, raw := w.e.do(method, api1+path, token, body, nil)
	return resp.StatusCode == 200 && strings.Contains(string(raw), marker)
}

// ---- 1. outbox ----

func TestOfficeLeakOutboxFollowsTheRequestOffice(t *testing.T) {
	w := newLeakWorld(t)
	e := w.e
	c := e.json("POST", api1+"/connections", w.owner, map[string]any{"provider": "google_gmail", "label": "Gmail asistente",
		"capabilities": []string{"mail.send"}, "mode": "simulated"}, 201)
	e.json("PUT", api1+"/connections/"+c["id"].(string)+"/grants/assistant", w.owner, map[string]any{"capabilities": []string{"mail.send"},
		"confirm_name": "Gmail asistente"}, 200)
	ids := map[string]string{}
	for _, tag := range []string{"SECRETO", "ABIERTO"} {
		out := w.gw.Execute(context.Background(), application.GatewayCall{Org: w.org, AgentID: "assistant", TaskID: "task-" + tag, RequestID: "req-" + tag,
			Tool: "email", Action: "send", Autonomy: "autonomous", Args: map[string]any{"to": "laura@acme.com", "subject": tag + "-correo", "body": "Texto"}})
		if out.Pending == nil {
			t.Fatalf("no pending send: %+v", out)
		}
		ids[tag] = out.Pending.OutboxID
	}
	for who, tok := range map[string]string{"admin outside the office": w.owner, "member of another office": w.ana, "nobody's": w.luis} {
		if w.sees(tok, "GET", "/outbox", "SECRETO-correo", nil) {
			t.Fatalf("%s: GET /outbox lists the private office's e-mail", who)
		}
		if !w.sees(tok, "GET", "/outbox", "ABIERTO-correo", nil) {
			t.Fatalf("%s: GET /outbox lacks General's e-mail", who)
		}
		if resp, raw := e.do("PATCH", api1+"/outbox/"+ids["SECRETO"], tok, map[string]any{"subject": "otro"}, nil); resp.StatusCode != 404 {
			t.Fatalf("%s: PATCH the private office's e-mail = %d %s", who, resp.StatusCode, raw)
		}
	}
	if !w.sees(w.chief, "GET", "/outbox", "SECRETO-correo", nil) {
		t.Fatal("the member of the office does not see its e-mail")
	}
	if resp, raw := e.do("PATCH", api1+"/outbox/"+ids["SECRETO"], w.chief, map[string]any{"subject": "SECRETO-editado"}, nil); resp.StatusCode != 200 {
		t.Fatalf("the member cannot edit the office's e-mail: %d %s", resp.StatusCode, raw)
	}
}

var _ = json.Marshal

// ---- 2. code runs ----

func TestOfficeLeakCodeRunsFollowTheTaskAndRequest(t *testing.T) {
	w := newLeakWorld(t)
	e, now := w.e, time.Now().UTC()
	for i, tag := range []string{"SECRETO", "ABIERTO"} { // the private office's run is the newest one
		run := sandbox.Run{ID: "run-" + tag, OrgID: w.org, AgentID: "assistant", TaskID: "task-" + tag, RequestID: "req-" + tag, ApprovalID: "ap-" + tag,
			Language: "python", Code: "print('" + tag + "-codigo')", Purpose: tag + "-proposito", CreatedAt: now.Add(-time.Duration(i) * time.Minute)}
		if err := e.code.Store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	for who, tok := range map[string]string{"admin outside the office": w.owner} {
		for _, p := range []string{"/code-runs", "/code-runs?limit=5", "/code-runs/run-SECRETO", "/code-runs?approval_id=ap-SECRETO"} {
			if w.sees(tok, "GET", p, "SECRETO", nil) {
				t.Fatalf("%s: GET %s shows the private office's code run", who, p)
			}
		}
		if !w.sees(tok, "GET", "/code-runs", "ABIERTO-proposito", nil) || !w.sees(tok, "GET", "/code-runs/run-ABIERTO", "ABIERTO-codigo", nil) ||
			!w.sees(tok, "GET", "/code-runs?approval_id=ap-ABIERTO", "ABIERTO-proposito", nil) {
			t.Fatalf("%s: General's code run is hidden too", who)
		}
		// the page is not left short by what is left out: the newest run is hidden, the limit still returns one
		if !w.sees(tok, "GET", "/code-runs?limit=1", "ABIERTO-proposito", nil) {
			t.Fatalf("%s: limit=1 returned nothing though a visible run exists", who)
		}
	}
	for _, p := range []string{"/code-runs", "/code-runs/run-SECRETO", "/code-runs?approval_id=ap-SECRETO"} {
		if !w.sees(w.chief, "GET", p, "SECRETO-proposito", nil) {
			t.Fatalf("the member of the office does not see GET %s", p)
		}
	}
}
