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
	is                      *application.InitiativeService
}

func newLeakWorld(t *testing.T) *leakWorld {
	t.Helper()
	w := &leakWorld{}
	w.e, w.owner, w.ana, w.marta, w.luis, _, w.soporte, w.org = officesWorldSealedOpts(t, func(o *opts) { o.rehearsal = true }, withGateway(&w.gw), func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			is := &application.InitiativeService{Store: d.Store.(*orgSpy).Store.(application.InitiativeStore), Submit: d.Orch, Cfg: d.Cfg, RequestOffice: d.Orch.OfficeOfRequest}
			w.is = is
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

// ---- 3. thought audit ----

func TestOfficeLeakTracesFollowTheTaskAndRequest(t *testing.T) {
	w := newLeakWorld(t)
	e, now := w.e, time.Now().UTC()
	add := func(tag string, n int, newest time.Time) {
		for i := n - 1; i >= 0; i-- { // oldest first: the store lists the last one added first
			tr := application.Trace{ID: tag + "-" + string(rune('a'+i)), AgentID: "assistant", TaskID: "task-" + tag, RequestID: "req-" + tag, Kind: "run-task",
				CreatedAt: newest.Add(-time.Duration(i) * time.Second), Status: "ok", Rationale: tag + "-razon"}
			if err := e.store.AddTrace(context.Background(), w.org, tr); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("ABIERTO", 3, now.Add(-time.Hour)) // older
	add("SECRETO", 3, now)                 // newer: the first page of the list is all private
	count := func(token, path, marker string) int {
		w.e.t.Helper()
		resp, raw := e.do("GET", api1+path, token, nil, nil)
		if resp.StatusCode != 200 {
			return -1
		}
		return strings.Count(string(raw), marker)
	}
	o := "/agents/assistant/traces"
	for _, p := range []string{o, o + "?limit=2", o + "/export", o + "/SECRETO-a"} {
		if n := count(w.owner, p, "SECRETO"); n > 0 {
			t.Fatalf("admin outside the office: GET %s shows %d private traces", p, n)
		}
	}
	if resp, _ := e.do("GET", api1+o+"/SECRETO-a", w.owner, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("a hidden trace by id = %d, want 404", resp.StatusCode)
	}
	if n := count(w.owner, o+"/ABIERTO-a", "ABIERTO-razon"); n != 1 {
		t.Fatalf("General's trace by id is hidden: %d", n)
	}
	// the pages are not left short by what is left out: limit=2 over 3 private + 3 open traces gives 2 open ones, and
	// following `before` gives the rest
	if n := count(w.owner, o+"?limit=2", "ABIERTO-razon"); n != 2 {
		t.Fatalf("limit=2 returned %d traces, want 2", n)
	}
	var page struct {
		Traces []application.Trace `json:"traces"`
	}
	_, raw := e.do("GET", api1+o+"?limit=2", w.owner, nil, nil)
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Traces) != 2 {
		t.Fatalf("page = %s", raw)
	}
	before := page.Traces[1].CreatedAt.Format(time.RFC3339Nano)
	if n := count(w.owner, o+"?limit=2&before="+before, "ABIERTO-razon"); n != 1 {
		t.Fatalf("the second page has %d traces, want the last 1", n)
	}
	if n := count(w.owner, o+"/export", "ABIERTO-razon"); n != 3 {
		t.Fatalf("the export has %d open traces, want 3", n)
	}
	// the member of the office reads everything
	for p, want := range map[string]int{o: 6, o + "/export": 6, o + "/SECRETO-a": 1} {
		if n := count(w.chief, p, "-razon"); n != want {
			t.Fatalf("the member: GET %s shows %d traces, want %d", p, n, want)
		}
	}
}

// ---- 4. stalled work ----

func TestOfficeLeakStalledWorkFollowsTheTaskOffice(t *testing.T) {
	w := newLeakWorld(t)
	e, now := w.e, time.Now().UTC()
	// a dependent of General's task that was asked for the private office: the cancellation of the first stops it
	if err := e.store.CreateTask(context.Background(), w.org, domain.Task{ID: "dep-ABIERTO", RequestID: "req-ABIERTO", Title: "SECRETO-dependiente", AgentID: "assistant",
		OfficeID: w.soporte, Status: domain.TaskPending, DependsOn: []string{"task-ABIERTO"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/stalled-work", "/tasks/task-ABIERTO/stall"} {
		if w.sees(w.owner, "GET", p, "SECRETO", nil) {
			t.Fatalf("admin outside the office: GET %s shows the private office's work", p)
		}
		if !w.sees(w.owner, "GET", p, "ABIERTO-tarea", nil) {
			t.Fatalf("admin outside the office: GET %s lacks General's stalled task", p)
		}
	}
	_, raw := e.do("GET", api1+"/tasks/task-ABIERTO/stall", w.owner, nil, nil)
	if !strings.Contains(string(raw), `"hidden_cascade":1`) {
		t.Fatalf("the hidden dependent is not counted: %s", raw)
	}
	if resp, _ := e.do("GET", api1+"/tasks/task-SECRETO/stall", w.owner, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("the stall of a hidden task = %d, want 404", resp.StatusCode)
	}
	// the scan names no title of work the person does not see
	scan := func(token string) string {
		resp, raw := e.do("POST", api1+"/stalled-work/scan", token, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("scan = %d %s", resp.StatusCode, raw)
		}
		return string(raw)
	}
	if got := scan(w.owner); strings.Contains(got, "SECRETO") || !strings.Contains(got, "ABIERTO-tarea") {
		t.Fatalf("the scan of an admin outside the office: %s", got)
	}
	// the member of the office reads it all
	for p, want := range map[string]string{"/stalled-work": "SECRETO-tarea", "/tasks/task-SECRETO/stall": "SECRETO-tarea"} {
		if !w.sees(w.chief, "GET", p, want, nil) {
			t.Fatalf("the member does not see GET %s", p)
		}
	}
	if !w.sees(w.chief, "GET", "/tasks/task-ABIERTO/stall", "SECRETO-dependiente", nil) {
		t.Fatal("the member does not see the dependent in the cascade")
	}
	if got := scan(w.chief); !strings.Contains(got, "SECRETO-tarea") {
		t.Fatalf("the scan of the member lacks the office's task: %s", got)
	}
}

// ---- 5. rehearsals ----

func TestOfficeLeakRehearsalsFollowTheRequestOffice(t *testing.T) {
	w := newLeakWorld(t)
	e, now := w.e, time.Now().UTC()
	for _, a := range []domain.RehearsalAction{
		{ID: "ra-SECRETO", RequestID: "req-SECRETO", TaskID: "task-SECRETO", Preview: map[string]string{"asunto": "SECRETO-ensayo"}},
		{ID: "ra-ABIERTO", RequestID: "req-ABIERTO", TaskID: "task-ABIERTO", Preview: map[string]string{"asunto": "ABIERTO-ensayo"}},
		// no request named: the task it came from still says the office
		{ID: "ra-TAREA", TaskID: "task-SECRETO", Preview: map[string]string{"asunto": "SECRETO-ensayo-tarea"}},
	} {
		a.AgentID, a.Tool, a.Action, a.Source, a.Scope, a.WouldDecision, a.Risk = "assistant", "email", "send", "gateway", "request", "approve", "high"
		a.Recipients, a.Flags, a.CreatedAt = []string{}, []string{}, now
		if err := e.store.AddRehearsalAction(context.Background(), w.org, a); err != nil {
			t.Fatal(err)
		}
	}
	for who, tok := range map[string]string{"admin outside the office": w.owner, "member of another office": w.ana, "nobody's": w.luis} {
		for _, p := range []string{"/rehearsal/actions", "/rehearsal/actions?limit=5", "/rehearsal/actions?request_id=req-SECRETO", "/rehearsal/actions?agent_id=assistant"} {
			if w.sees(tok, "GET", p, "SECRETO", nil) {
				t.Fatalf("%s: GET %s shows the private office's rehearsal", who, p)
			}
		}
		if !w.sees(tok, "GET", "/rehearsal/actions", "ABIERTO-ensayo", nil) {
			t.Fatalf("%s: General's rehearsal is hidden", who)
		}
		if w.sees(tok, "GET", "/rehearsal/agents", "SECRETO", nil) {
			t.Fatalf("%s: /rehearsal/agents shows the private office's rehearsal", who)
		}
	}
	if resp, _ := e.do("POST", api1+"/rehearsal/actions/ra-SECRETO/verdict", w.owner, map[string]any{"verdict": "would_approve"}, nil); resp.StatusCode != 404 {
		t.Fatalf("a verdict on a hidden rehearsal = %d, want 404", resp.StatusCode)
	}
	for _, p := range []string{"/rehearsal/actions", "/rehearsal/actions?request_id=req-SECRETO"} {
		if !w.sees(w.chief, "GET", p, "SECRETO-ensayo", nil) {
			t.Fatalf("the member does not see GET %s", p)
		}
	}
	if !w.sees(w.chief, "GET", "/rehearsal/actions", "SECRETO-ensayo-tarea", nil) {
		t.Fatal("the member does not see the rehearsal that names only its task")
	}
}

// ---- 6. suggestions ----

// A suggestion about a failed or stalled task names it: it follows the office of that task (list, accept, dismiss), and
// the request accepting it starts is asked from that office.
func TestOfficeLeakSuggestionsFollowTheTaskOffice(t *testing.T) {
	w := newLeakWorld(t)
	e, ctx := w.e, application.WithOrg(context.Background(), w.org)
	for _, tag := range []string{"SECRETO", "ABIERTO"} {
		w.is.TaskFailed(ctx, domain.Task{ID: "task-" + tag, RequestID: "req-" + tag, Title: tag + "-tarea", AgentID: "assistant"}, nil)
	}
	its, err := w.is.List(ctx, application.InitiativePending, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, it := range its {
		_, req := it.Work()
		ids[req] = it.ID
	}
	if ids["req-SECRETO"] == "" || ids["req-ABIERTO"] == "" {
		t.Fatalf("suggestions = %+v", its)
	}
	for who, tok := range map[string]string{"admin outside the office": w.owner, "member of another office": w.ana, "nobody's": w.luis} {
		if w.sees(tok, "GET", "/initiatives", "SECRETO", nil) {
			t.Fatalf("%s: GET /initiatives names the private office's task", who)
		}
		if !w.sees(tok, "GET", "/initiatives", "ABIERTO-tarea", nil) {
			t.Fatalf("%s: GET /initiatives lacks General's", who)
		}
		for _, act := range []string{"accept", "dismiss"} {
			if c := e.status("POST", api1+"/initiatives/"+ids["req-SECRETO"]+"/"+act, tok, nil); c != 404 {
				t.Fatalf("%s: %s the private office's suggestion = %d, want 404", who, act, c)
			}
		}
	}
	if !w.sees(w.chief, "GET", "/initiatives", "SECRETO-tarea", nil) {
		t.Fatal("the member does not see the office's suggestion")
	}
	resp, raw := e.do("POST", api1+"/initiatives/"+ids["req-SECRETO"]+"/accept", w.chief, nil, nil)
	var it application.Initiative
	_ = json.Unmarshal(raw, &it)
	if resp.StatusCode != 202 || it.RequestID == "" {
		t.Fatalf("accept = %d %s", resp.StatusCode, raw)
	}
	req, err := e.store.GetRequest(ctx, w.org, it.RequestID)
	if err != nil || req.OfficeID != w.soporte {
		t.Fatalf("the accepted request is asked from %q (%v), want the private office", req.OfficeID, err)
	}
	if w.sees(w.owner, "GET", "/requests", "SECRETO-tarea", nil) {
		t.Fatal("the accepted request shows the private office's task to who is not in it")
	}
}
