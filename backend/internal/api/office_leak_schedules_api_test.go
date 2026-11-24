package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// A routine created from a private office is that office's work. GET /schedules leaves it out for who is not in the
// office (an admin who is not a member included), and so do the routes by id: editing or deleting it answers 404, as a
// routine that does not exist, and changes nothing. A member of the office, and everybody for General's routines, are
// not affected.
func TestOfficeLeakSchedulesAPI(t *testing.T) {
	cs := memory.New() // the routines' own store: the one the routes of /schedules read
	e, owner, ana, marta, luis, _, soporte, org := officesWorldSealed(t, func(x *api.Deps) {
		x.OrgConfig = &application.OrgConfig{Cfg: x.Cfg, Store: cs, Core: x.Store, Orch: x.Orch, Rec: x.Rec, Log: x.Log}
	})
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	// an admin who heads a department of its own: it is in that office and not in Soporte, and it may manage routines
	boss := e.member(&auth.Session{AccessToken: owner, OrgID: org, User: auth.UserView{ID: e.userID(owner)}}, "boss-sch@example.com", auth.RoleAdmin)
	legal := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": e.userID(boss)}}, 201)["id"].(string)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": legal}, 201)
	e.json("PUT", api1+"/offices/"+legal, owner, map[string]any{"visibility": "private"}, 200)

	ctx, now := context.Background(), time.Now().UTC()
	seed := func(id, office string) {
		t.Helper()
		next := now.Add(time.Hour)
		if err := cs.PutSchedule(ctx, org, application.Schedule{ID: id, TemplateKey: "daily_briefing", Params: map[string]string{}, Hour: 9, Timezone: "UTC",
			Weekdays: []int{}, Enabled: true, NextRunAt: &next, CreatedAt: now, OfficeID: office}); err != nil {
			t.Fatal(err)
		}
	}
	seed("sch-SECRETO", soporte)
	seed("sch-ABIERTA", "")
	has := func(token, id string) bool {
		t.Helper()
		_, raw := e.do("GET", api1+"/schedules", token, nil, nil)
		return strings.Contains(string(raw), id)
	}

	// the listing: the member of the office sees it (so the rest is not empty), nobody else does
	if !has(marta, "sch-SECRETO") {
		t.Fatalf("the member of the office does not see its routine")
	}
	for name, tok := range map[string]string{"admin not in the office": owner, "member of another office": ana, "nobody's": luis, "admin of another office": boss} {
		if has(tok, "sch-SECRETO") {
			t.Fatalf("%s: GET /schedules lists the private office's routine", name)
		}
		if !has(tok, "sch-ABIERTA") {
			t.Fatalf("%s: GET /schedules lacks General's routine", name)
		}
	}

	// by id: for who is not in the office it answers as a missing routine and leaves it as it was
	body := map[string]any{"hour": 3, "minute": 30, "timezone": "UTC"}
	for name, tok := range map[string]string{"admin not in the office": owner, "admin of another office": boss} {
		miss, _ := e.do("PUT", api1+"/schedules/sch-NOEXISTE", tok, body, nil)
		if miss.StatusCode != 404 {
			t.Fatalf("%s: PUT of a missing routine = %d, want 404", name, miss.StatusCode)
		}
		if resp, raw := e.do("PUT", api1+"/schedules/sch-SECRETO", tok, body, nil); resp.StatusCode != 404 {
			t.Fatalf("%s: PUT /schedules/{id} of the private office's routine = %d, want 404: %.300s", name, resp.StatusCode, raw)
		}
		if resp, raw := e.do("DELETE", api1+"/schedules/sch-SECRETO", tok, nil, nil); resp.StatusCode != 404 {
			t.Fatalf("%s: DELETE /schedules/{id} of the private office's routine = %d, want 404: %.300s", name, resp.StatusCode, raw)
		}
	}
	got, _ := cs.GetSchedule(ctx, org, "sch-SECRETO")
	if got.ID == "" || got.Hour != 9 {
		t.Fatalf("a refused request changed or deleted the routine: %+v", got)
	}
	// General's routine is anybody's admin to edit: the guard does not close what is not sealed
	if resp, raw := e.do("PUT", api1+"/schedules/sch-ABIERTA", boss, body, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT of General's routine = %d: %.300s", resp.StatusCode, raw)
	}
}
