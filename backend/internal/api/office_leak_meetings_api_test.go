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
	"aiworkforce/backend/internal/calendar"
	"aiworkforce/backend/internal/meetings"
)

// A meeting created from a private office is that office's work: its members see it, and so does who organized it
// (seesMeeting). Everybody else (an admin who is not in the office included) finds it neither in GET /meetings nor by
// id: every route about it answers 404, as a meeting that does not exist, and changes nothing.
func TestOfficeLeakMeetingsAPI(t *testing.T) {

	var msvc *meetings.Service
	e := newEnv(t, opts{auth: true, orgchart: true, away: true, v2: true, ws: true, deps: func(x *api.Deps) {
		withOfficesMulti(x)
		x.Commands = &api.CommandsDeps{}
		msvc = meetings.New(meetings.Config{Store: meetings.NewMemStore(), Calendars: meetCals{fake: calendar.NewFake(calendar.ProviderGoogle)},
			Summarizer: meetSummary{orch: x.Orch}, Tasks: meetTasks{orch: x.Orch}, Agents: meetAgents{st: x.Store}, JitsiBase: "https://jitsi.example.org"})
		x.Meetings = msvc
	}})
	o := e.register("owner-mt@example.com", "OwnerMT")
	owner, org := o.AccessToken, o.OrgID
	ana := e.member(o, "ana-mt@example.com", auth.RoleMember)
	marta := e.member(o, "marta-mt@example.com", auth.RoleMember)
	luis := e.member(o, "luis-mt@example.com", auth.RoleMember)
	soporte := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Soporte", "head": map[string]any{"kind": "person", "id": e.userID(marta)}}, 201)["id"].(string)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": soporte}, 201)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)

	ctx := application.WithOrg(context.Background(), org)
	start := time.Now().UTC().Add(48 * time.Hour)
	mk := func(office, title, organizer string) string {
		t.Helper()
		m, err := msvc.Create(application.WithRequestOffice(ctx, office), org, organizer, meetings.CreateInput{Title: title, Start: start, End: start.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	// SECRETO-a: organized by ana (who is not in the office); SECRETO-b: organized by marta (who is its head)
	secA := mk(soporte, "SECRETO-a", e.userID(ana))
	secB := mk(soporte, "SECRETO-b", e.userID(marta))
	open := mk("", "ABIERTA", e.userID(luis))

	titles := func(token string) string {
		t.Helper()
		_, raw := e.do("GET", api1+"/meetings", token, nil, nil)
		return string(raw)
	}
	// the listing
	if got := titles(marta); !strings.Contains(got, "SECRETO-a") || !strings.Contains(got, "SECRETO-b") {
		t.Fatalf("the member of the office lacks its meetings: %.400s", got)
	}
	if got := titles(ana); !strings.Contains(got, "SECRETO-a") || strings.Contains(got, "SECRETO-b") {
		t.Fatalf("the organizer sees exactly the meeting she organized: %.400s", got)
	}
	for name, tok := range map[string]string{"admin not in the office": owner, "nobody's": luis} {
		got := titles(tok)
		if strings.Contains(got, "SECRETO") {
			t.Fatalf("%s: GET /meetings lists a private office's meeting: %.400s", name, got)
		}
		if !strings.Contains(got, "ABIERTA") {
			t.Fatalf("%s: GET /meetings lacks the open meeting: %.400s", name, got)
		}
	}

	// every route by id: 404 for who does not see the meeting, the same as for one that does not exist
	type route struct {
		method, suffix string
		body           any
	}
	routes := []route{{"GET", "", nil}, {"PATCH", "", map[string]any{"title": "HACKEADA"}}, {"POST", "/start", map[string]any{}}, {"POST", "/end", map[string]any{}},
		{"POST", "/cancel", map[string]any{}}, {"POST", "/brief", map[string]any{}}, {"POST", "/sync", map[string]any{}}, {"GET", "/ics", nil},
		{"GET", "/minutes", nil}, {"POST", "/minutes", map[string]any{"notes": "x"}}, {"PUT", "/minutes", map[string]any{"text": "x"}}, {"POST", "/minutes/tasks", map[string]any{}}}
	for _, who := range []struct{ name, token, id string }{{"admin not in the office", owner, secA}, {"nobody's", luis, secA}, {"nobody's", luis, secB},
		{"the organizer of the other meeting", ana, secB}, {"admin not in the office", owner, secB}} {
		for _, r := range routes {
			path := api1 + "/meetings/" + who.id + r.suffix
			resp, raw := e.do(r.method, path, who.token, r.body, nil)
			if resp.StatusCode != 404 {
				t.Fatalf("%s: %s %s = %d, want 404: %.300s", who.name, r.method, path, resp.StatusCode, raw)
			}
			miss, _ := e.do(r.method, api1+"/meetings/mt-NOEXISTE"+r.suffix, who.token, r.body, nil)
			if miss.StatusCode != resp.StatusCode {
				t.Fatalf("%s: %s %s answers %d, a missing meeting %d", who.name, r.method, path, resp.StatusCode, miss.StatusCode)
			}
		}
	}
	for _, id := range []string{secA, secB} {
		m, err := msvc.Get(ctx, org, id)
		if err != nil || m.Status != meetings.StatusScheduled || !strings.HasPrefix(m.Title, "SECRETO") {
			t.Fatalf("a refused request changed the meeting: %+v %v", m, err)
		}
	}

	// the members of the office, and who organized the meeting, do get in
	for _, ok := range []struct{ name, token, id string }{{"member", marta, secA}, {"member", marta, secB}, {"organizer", ana, secA}} {
		for _, p := range []string{"", "/ics"} {
			if resp, raw := e.do("GET", api1+"/meetings/"+ok.id+p, ok.token, nil, nil); resp.StatusCode != 200 {
				t.Fatalf("%s: GET /meetings/%s%s = %d: %.300s", ok.name, ok.id, p, resp.StatusCode, raw)
			}
		}
	}
	if resp, raw := e.do("PATCH", api1+"/meetings/"+secA, ana, map[string]any{"title": "SECRETO-a2"}, nil); resp.StatusCode != 200 {
		t.Fatalf("the organizer cannot edit her meeting: %d %.300s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("POST", api1+"/meetings/"+secB+"/cancel", marta, map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("the member cannot cancel the office's meeting: %d %.300s", resp.StatusCode, raw)
	}
	// the rooms: whoever does not see a private office's meeting still sees the room taken (start and end), without
	// its id or its title; who sees it gets all of it
	var rooms []map[string]any
	if raw := e.do2("GET", api1+"/meetings/rooms", owner); json.Unmarshal(raw, &rooms) != nil || len(rooms) == 0 {
		t.Fatalf("no rooms: %s", raw)
	}
	roomID, _ := rooms[0]["id"].(string)
	soon := time.Now().UTC().Add(30 * time.Minute)
	rm, err := msvc.Create(application.WithRequestOffice(ctx, soporte), org, e.userID(marta), meetings.CreateInput{Title: "SECRETO-sala", Room: roomID, Start: soon, End: soon.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	roomOf := func(token string) (string, map[string]any) {
		raw := e.do2("GET", api1+"/meetings/rooms", token)
		var rs []map[string]any
		_ = json.Unmarshal(raw, &rs)
		for _, r := range rs {
			if r["id"] == roomID {
				next, _ := r["next"].(map[string]any)
				return string(raw), next
			}
		}
		t.Fatalf("room %s missing: %s", roomID, raw)
		return "", nil
	}
	for name, tok := range map[string]string{"admin not in the office": owner, "nobody's": luis, "organizer of another": ana} {
		raw, next := roomOf(tok)
		if strings.Contains(raw, "SECRETO") || strings.Contains(raw, rm.ID) {
			t.Fatalf("%s: GET /meetings/rooms shows the private meeting: %.500s", name, raw)
		}
		if next == nil || next["start"] == nil || next["end"] == nil || next["start"] == "" {
			t.Fatalf("%s: the room does not show as taken: %.500s", name, raw)
		}
	}
	if _, next := roomOf(marta); next == nil || next["id"] != rm.ID || next["title"] != "SECRETO-sala" {
		t.Fatalf("the member does not see the meeting of the room: %v", next)
	}
	// General's meeting is everybody's
	for _, tok := range []string{owner, ana, luis, marta} {
		if resp, raw := e.do("GET", api1+"/meetings/"+open, tok, nil, nil); resp.StatusCode != 200 {
			t.Fatalf("the open meeting: %d %.300s", resp.StatusCode, raw)
		}
	}
}

// do2 is a GET that returns the body.
func (e *env) do2(method, path, token string) []byte {
	e.t.Helper()
	_, raw := e.do(method, path, token, nil, nil)
	return raw
}
