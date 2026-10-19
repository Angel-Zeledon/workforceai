package api_test

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

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/calendar"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/meetings"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/roles"
)

// Meetings over HTTP (docs/architecture/meetings.md). The runtime is the cmdRT double (it records what
// the summarize call receives; no model is called), the calendar is calendar.Fake behind a connection
// resolver double, and the drive is the in-memory one.

type meetCals struct{ fake *calendar.Fake }

func (m meetCals) List(context.Context, string) ([]meetings.CalendarInfo, error) {
	return []meetings.CalendarInfo{{ConnectionID: "cn_cal", Provider: calendar.ProviderGoogle, Label: "Work", Account: "ana@x.com", Class: "write", Status: "active"}}, nil
}
func (m meetCals) Client(_ context.Context, _, id string) (calendar.Client, error) {
	if id != "cn_cal" {
		return nil, calendar.ErrNotConfigured
	}
	return m.fake, nil
}

type meetSummary struct{ orch *application.Orchestrator }

func (m meetSummary) Summarize(ctx context.Context, org string, in meetings.SummaryIn) (meetings.SummaryOut, error) {
	items := make([]application.ChatHistoryItem, 0, len(in.Messages))
	for _, msg := range in.Messages {
		items = append(items, application.ChatHistoryItem{From: msg[0], Text: msg[1]})
	}
	res, err := m.orch.Summarize(application.WithOrg(ctx, org), "", application.SummarizeRequest{Mode: "meeting", Messages: items, Topic: in.Title, Participants: in.Participants, Locale: in.Locale})
	if err != nil {
		return meetings.SummaryOut{}, err
	}
	return meetings.SummaryOut{Text: res.Text, Source: res.Source}, nil
}

type meetTasks struct{ orch *application.Orchestrator }

func (m meetTasks) CreateTasks(ctx context.Context, org string, b meetings.TaskBatch) (string, error) {
	plan := application.PlanResponse{Objectives: b.Objectives}
	for _, t := range b.Tasks {
		plan.Tasks = append(plan.Tasks, application.PlannedTask{Key: t.Key, Title: t.Title, Description: t.Description, AgentID: t.AgentID})
	}
	return m.orch.SubmitPlan(application.WithOrg(ctx, org), b.Text, plan)
}

type meetFiles struct{ d *drive.Service }

func (m meetFiles) Save(ctx context.Context, org, actor, path string, data []byte, msg string) (string, string, error) {
	r, err := m.d.Write(application.WithOrg(ctx, org), drive.Human(actor), drive.WriteInput{Path: path, Data: data, Message: msg, Source: "meetings"})
	if err != nil {
		return "", "", err
	}
	return r.File.ID, r.File.Path, nil
}

type meetAgents struct{ st application.Store }

func (m meetAgents) Agents(ctx context.Context, org string) ([]meetings.AgentInfo, error) {
	as, err := m.st.ListAgents(application.WithOrg(ctx, org), org)
	var out []meetings.AgentInfo
	for _, a := range as {
		out = append(out, meetings.AgentInfo{ID: a.ID, Name: a.Name, Role: string(a.Role)})
	}
	return out, err
}

type meetEnv struct {
	*env
	rt                    *cmdRT
	dr                    *drive.Service
	cal                   *calendar.Fake
	owner, member, viewer string
	other                 string // a member of ANOTHER organization
}

func newMeetEnv(t *testing.T) *meetEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt := &cmdRT{}
	orch := application.NewOrchestrator(ctx, cfg, mem, rt, memory.NewLocker(), rec, appr, q, log)
	orch.SetConvContext(mem)
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: cfg.OrgID, Log: log})
	cal := calendar.NewFake(calendar.ProviderGoogle)
	msvc := meetings.New(meetings.Config{Store: meetings.NewMemStore(), Calendars: meetCals{fake: cal}, Summarizer: meetSummary{orch: orch}, Tasks: meetTasks{orch: orch},
		Files: meetFiles{d: dr}, Agents: meetAgents{st: mem}, JitsiBase: "https://jitsi.example.org"})

	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: rt, Hub: hub, Log: log, Rec: rec, AuthEnabled: true,
		Commands: &api.CommandsDeps{}, Drive: dr, Meetings: msvc}
	svc, err := auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	d.Auth = svc
	d.AuthRoutes = auth.NewHandler(svc, auth.HandlerConfig{}).Routes()
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	e := &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}
	me := &meetEnv{env: e, rt: rt, dr: dr, cal: cal}
	owner := e.register("owner-meet@example.com", "OwnerMeet")
	me.owner = owner.AccessToken
	me.member = e.member(owner, "member-meet@example.com", auth.RoleMember)
	me.viewer = e.member(owner, "viewer-meet@example.com", auth.RoleViewer)
	me.other = e.register("other-meet@example.com", "OtherMeet").AccessToken
	return me
}

func (e *meetEnv) json(method, path, token string, body any, want int) map[string]any {
	e.t.Helper()
	resp, raw := e.do(method, api1+path, token, body, nil)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d (want %d): %s", method, path, resp.StatusCode, want, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func inHours(h int) string {
	return time.Now().UTC().Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
}

func TestMeetingsRBACAndLifecycleOverHTTP(t *testing.T) {
	e := newMeetEnv(t)
	body := map[string]any{"title": "Pricing review", "room": "boardroom", "start": inHours(1), "end": inHours(2),
		"attendees": []map[string]any{{"kind": "agent", "id": "sales"}, {"kind": "agent", "id": "legal"}},
		"agenda":    []map[string]any{{"title": "Numbers", "minutes": 20, "owner": "sales"}}}
	e.json("POST", "/meetings", e.viewer, body, 403)
	m := e.json("POST", "/meetings", e.member, body, 201)
	id := m["id"].(string)
	if m["status"] != "scheduled" || !strings.HasPrefix(m["join_url"].(string), "https://jitsi.example.org/pricing-review-") || m["embeddable"] != true {
		t.Fatalf("created: %v", m)
	}
	// viewers read, members cannot touch settings, owners can
	resp, raw := e.do("GET", api1+"/meetings", e.viewer, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "Pricing review") {
		t.Fatalf("viewer list: %d %s", resp.StatusCode, raw)
	}
	e.json("PUT", "/meetings/settings", e.member, map[string]any{"default_provider": "zoom"}, 403)
	e.json("PUT", "/meetings/settings", e.owner, map[string]any{"jitsi_base_url": "https://meet.corp.example/"}, 200)
	// a second meeting in the same room and time is refused with a stable code
	clash := e.json("POST", "/meetings", e.member, map[string]any{"title": "Clash", "room": "boardroom", "start": inHours(1), "end": inHours(2)}, 409)
	if clash["code"] != "room_busy" {
		t.Fatalf("%v", clash)
	}
	// rooms view: the room shows as next
	resp, raw = e.do("GET", api1+"/meetings/rooms", e.viewer, nil, nil)
	var rooms []map[string]any
	_ = json.Unmarshal(raw, &rooms)
	var boardroom map[string]any
	for _, r := range rooms {
		if r["id"] == "boardroom" {
			boardroom = r
		}
	}
	if resp.StatusCode != 200 || boardroom == nil || boardroom["next"] == nil {
		t.Fatalf("rooms: %d %s", resp.StatusCode, raw)
	}
	// state machine through HTTP
	e.json("POST", "/meetings/"+id+"/end", e.member, nil, 409)
	live := e.json("POST", "/meetings/"+id+"/start", e.member, map[string]any{"brief_agents": true, "lang": "en"}, 200)
	if live["status"] != "live" || live["external"].(map[string]any)["brief_request_id"] == "" {
		t.Fatalf("start with briefing: %v", live)
	}
	e.json("POST", "/meetings/"+id+"/start", e.member, nil, 409)
	e.json("POST", "/meetings/"+id+"/end", e.member, nil, 200)

	// minutes from notes: the runtime receives mode "meeting" with the notes as DATA messages
	notes := "Decisions:\n- Raise the price 5%\nAction items:\n- Valeria: send the quote to Acme by 2026-11-02\n- Elena: review the contract"
	e.json("POST", "/meetings/"+id+"/minutes", e.viewer, map[string]any{"notes": notes}, 403)
	min := e.json("POST", "/meetings/"+id+"/minutes", e.member, map[string]any{"notes": notes, "lang": "en"}, 200)
	if len(min["decisions"].([]any)) != 1 || len(min["action_items"].([]any)) != 2 || !strings.HasPrefix(min["file_path"].(string), "/Shared/Meetings/") {
		t.Fatalf("minutes: %v", min)
	}
	e.rt.mu.Lock()
	sums := append([]application.SummarizeRequest(nil), e.rt.sums...)
	e.rt.mu.Unlock()
	if len(sums) != 1 || sums[0].Mode != "meeting" || sums[0].Topic != "Pricing review" || sums[0].Locale != "en" || len(sums[0].Participants) != 2 {
		t.Fatalf("summarize call: %+v", sums)
	}
	f, err := e.dr.FileByPath(application.WithOrg(context.Background(), e.orgOf(e.owner)), min["file_path"].(string))
	if err != nil {
		t.Fatalf("minutes file in the drive: %v", err)
	}
	if txt, _ := e.dr.ReadText(application.WithOrg(context.Background(), e.orgOf(e.owner)), f, 0, 0, 100000); !strings.Contains(txt.Text, "Raise the price 5%") {
		t.Fatalf("file content: %q", txt.Text)
	}
	// explicit step: action items become tasks of the right agents
	before := len(e.rt.planTexts())
	res := e.json("POST", "/meetings/"+id+"/minutes/tasks", e.member, map[string]any{"lang": "en"}, 200)
	if res["request_id"] == "" || before != len(e.rt.planTexts()) {
		t.Fatalf("tasks must be created from the plan we pass, not planned by the model: %v", res)
	}
	agents := map[string]bool{}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !(agents["sales"] && agents["legal"]); time.Sleep(20 * time.Millisecond) {
		tasks, _ := e.store.ListTasks(context.Background(), e.orgOf(e.owner), "", "")
		for _, tk := range tasks {
			if strings.Contains(tk.Description, "Acme") || strings.Contains(tk.Description, "contract") {
				agents[tk.AgentID] = true
			}
		}
	}
	if !agents["sales"] || !agents["legal"] {
		t.Fatalf("Valeria's and Elena's items go to sales and legal: %v", agents)
	}
	e.json("POST", "/meetings/"+id+"/minutes/tasks", e.member, nil, 422)
	got := e.json("GET", "/meetings/"+id+"/minutes", e.viewer, nil, 200)
	if got["request_id"] == "" {
		t.Fatalf("%v", got)
	}
	// another organization sees nothing
	e.json("GET", "/meetings/"+id, e.other, nil, 404)
	e.json("GET", "/meetings/"+id+"/minutes", e.other, nil, 404)
}

func (e *meetEnv) orgOf(token string) string {
	e.t.Helper()
	p, err := e.svc.Authenticate(context.Background(), token)
	if err != nil {
		e.t.Fatal(err)
	}
	return p.OrgID
}

func TestMeetingsCalendarSyncFreeBusyAndICSOverHTTP(t *testing.T) {
	e := newMeetEnv(t)
	e.json("PUT", "/meetings/settings", e.owner, map[string]any{"calendar_connection_id": "cn_cal"}, 200)
	e.json("PUT", "/meetings/settings", e.owner, map[string]any{"calendar_connection_id": "cn_nope"}, 400)
	cals := e.json("GET", "/meetings/calendars", e.owner, nil, 200)
	_ = cals
	resp, raw := e.do("GET", api1+"/meetings/calendars", e.owner, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "cn_cal") {
		t.Fatalf("calendars: %d %s", resp.StatusCode, raw)
	}
	m := e.json("POST", "/meetings", e.member, map[string]any{"title": "Board", "start": inHours(24), "end": inHours(25), "provider": "meet", "sync_calendar": true,
		"attendees": []map[string]any{{"kind": "external", "name": "Bob", "email": "bob@acme.com"}}}, 201)
	ext := m["external"].(map[string]any)
	if ext["event_id"] == "" || !strings.HasPrefix(m["join_url"].(string), "https://meet.google.com/") {
		t.Fatalf("synced with a Meet link from the calendar: %v", m)
	}
	if evs := e.cal.Snapshot(); len(evs) != 1 || evs[0].Attendees[0] != "bob@acme.com" {
		t.Fatalf("calendar event: %+v", evs)
	}
	// free/busy needs a member (it exposes colleagues' calendars)
	e.cal.BusyOf["bob@acme.com"] = []calendar.Busy{{Start: time.Now().UTC().Add(2 * time.Hour), End: time.Now().UTC().Add(3 * time.Hour)}}
	fbBody := map[string]any{"from": inHours(0), "to": inHours(72), "emails": []string{"bob@acme.com"}, "duration_min": 60}
	e.json("POST", "/meetings/free-busy", e.viewer, fbBody, 403)
	fb := e.json("POST", "/meetings/free-busy", e.member, fbBody, 200)
	if fb["provider"] != calendar.ProviderGoogle || len(fb["busy"].(map[string]any)["bob@acme.com"].([]any)) != 1 {
		t.Fatalf("free busy: %v", fb)
	}
	evs := e.json("GET", "/meetings/calendar-events", e.member, nil, 200)
	_ = evs

	// single-meeting ICS download
	resp, raw = e.do("GET", api1+"/meetings/"+m["id"].(string)+"/ics", e.viewer, nil, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") || !strings.Contains(string(raw), "BEGIN:VEVENT") || !strings.Contains(string(raw), "SUMMARY:Board") {
		t.Fatalf("ics: %d %s", resp.StatusCode, raw)
	}
	// subscription feed: the token is shown once, the endpoint is public, revoking kills it
	feed := e.json("POST", "/meetings/feeds", e.member, map[string]any{"label": "phone"}, 201)
	path := feed["path"].(string)
	if !strings.HasPrefix(path, "/api/v1/calendar/feed/") || !strings.HasSuffix(path, ".ics") {
		t.Fatalf("%v", feed)
	}
	resp, raw = e.do("GET", path, "", nil, nil) // no bearer token
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "SUMMARY:Board") {
		t.Fatalf("public feed: %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("GET", "/api/v1/calendar/feed/"+strings.Repeat("a", 40)+".ics", "", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("a wrong token is a plain 404: %d %s", resp.StatusCode, raw)
	}
	if resp, _ = e.do("GET", strings.TrimSuffix(path, ".ics"), "", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("the .ics suffix is part of the contract: %d", resp.StatusCode)
	}
	lst, raw := e.do("GET", api1+"/meetings/feeds", e.member, nil, nil)
	if lst.StatusCode != 200 || strings.Contains(string(raw), feed["token"].(string)) || strings.Contains(string(raw), "token_hash") {
		t.Fatalf("listing never returns the token: %s", raw)
	}
	fid := feed["feed"].(map[string]any)["id"].(string)
	e.json("DELETE", "/meetings/feeds/"+fid, e.viewer, nil, 403)
	if resp, _ = e.do("DELETE", api1+"/meetings/feeds/"+fid, e.member, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ = e.do("GET", path, "", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("revoked feed: %d", resp.StatusCode)
	}
	// cancelling removes the calendar event
	e.json("POST", "/meetings/"+m["id"].(string)+"/cancel", e.member, nil, 200)
	if len(e.cal.Snapshot()) != 0 {
		t.Fatal("event must be deleted on cancel")
	}
}

func TestMeetingCommands(t *testing.T) {
	e := newMeetEnv(t)
	run := func(token string, body map[string]any, want int) map[string]any {
		t.Helper()
		resp, raw := e.do("POST", api1+"/commands/run", token, body, nil)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if resp.StatusCode != want {
			t.Fatalf("run %v = %d %s", body, resp.StatusCode, raw)
		}
		return out
	}
	// /sala opens a live meeting with the named agents in a free room
	out := run(e.member, map[string]any{"raw": "/sala ventas legal: revisar precios", "lang": "es", "context": map[string]any{"surface": "office"}}, 200)
	res := out["result"].(map[string]any)
	if res["kind"] != "navigate" || !strings.HasPrefix(res["href"].(string), "/meetings?open=mt_") {
		t.Fatalf("%v", out)
	}
	list := e.json("GET", "/meetings?status=live", e.member, nil, 200)
	_ = list
	resp, raw := e.do("GET", api1+"/meetings?status=live", e.member, nil, nil)
	var live []map[string]any
	_ = json.Unmarshal(raw, &live)
	if resp.StatusCode != 200 || len(live) != 1 || live[0]["title"] != "revisar precios" || live[0]["room"] == "" || len(live[0]["attendees"].([]any)) != 2 {
		t.Fatalf("live meeting: %s", raw)
	}
	// the room is now taken: the second /room goes to another room
	run(e.member, map[string]any{"raw": "/room sales: second", "lang": "en", "context": map[string]any{"surface": "office"}}, 200)
	_, raw = e.do("GET", api1+"/meetings?status=live", e.member, nil, nil)
	_ = json.Unmarshal(raw, &live)
	if len(live) != 2 || live[0]["room"] == live[1]["room"] {
		t.Fatalf("each live meeting gets its own room: %s", raw)
	}
	// /agendar navigates to the dialog with agents resolved to ids
	out = run(e.member, map[string]any{"raw": "/agendar ventas legal: revisar precios", "lang": "es", "context": map[string]any{"surface": "office"}}, 200)
	href := out["result"].(map[string]any)["href"].(string)
	if !strings.HasPrefix(href, "/meetings?") || !strings.Contains(href, "new=1") || !strings.Contains(href, "agents=sales%2Clegal") {
		t.Fatalf("schedule href: %s", href)
	}
	// /acta writes the minutes of the latest live meeting from typed notes
	out = run(e.member, map[string]any{"raw": "/acta Decidimos subir el precio. TODO: Ana envía la cotización", "lang": "es", "context": map[string]any{"surface": "office"}}, 200)
	data := out["result"].(map[string]any)["data"].(map[string]any)
	if data["decisions"].(float64) != 1 || data["action_items"].(float64) != 1 || !strings.HasPrefix(data["file_path"].(string), "/Shared/Meetings/") {
		t.Fatalf("minutes command: %v", out)
	}
	// viewers cannot run any of them; the catalog lists the three commands
	run(e.viewer, map[string]any{"raw": "/sala legal: x", "lang": "es", "context": map[string]any{"surface": "office"}}, 403)
	resp, raw = e.do("GET", api1+"/commands", e.member, nil, nil)
	for _, id := range []string{`"id":"room"`, `"id":"schedule"`, `"id":"minutes"`} {
		if resp.StatusCode != 200 || !strings.Contains(string(raw), id) {
			t.Fatalf("catalog misses %s", id)
		}
	}
}

func TestMeetingsFeatureOffWhenNotWired(t *testing.T) {
	// An API built without Deps.Meetings must not mount any of the routes (404, never a panic).
	cfg := application.DefaultConfig()
	mem := memory.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orch := application.NewOrchestrator(ctx, cfg, mem, &cmdRT{}, memory.NewLocker(), rec, appr, q, log)
	ts := httptest.NewServer(api.NewRouter(api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: &cmdRT{}, Hub: hub, Log: log, Rec: rec}))
	defer ts.Close()
	for _, p := range []string{"/api/v1/meetings", "/api/v1/calendar/feed/x.ics"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, resp.StatusCode)
		}
	}
	_ = errors.New
}
