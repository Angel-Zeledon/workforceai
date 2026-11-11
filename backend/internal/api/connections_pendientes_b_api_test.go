package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/sanitize"
)

// D3 (pendientes, área B, punto 6): the connections of a department through the API.

// countingConns counts the reads of one connection (GetConnection).
type countingConns struct {
	connections.Store
	gets *atomic.Int32
}

func (c countingConns) GetConnection(ctx context.Context, org, id string) (connections.Connection, error) {
	c.gets.Add(1)
	return c.Store.GetConnection(ctx, org, id)
}

// Every answer that returns a connection says whether the person may manage it: the lead of Legal (a plain member)
// suspends, resumes and revokes its connection and is told can_manage, like in the listing.
func TestConnectionAnswersSayWhoManages(t *testing.T) {
	o := newOfficeConns(t)
	legal := o.create(o.ana, "Base de Legal", o.office, 201)
	for _, step := range []struct {
		path string
		body any
	}{{"/suspend", nil}, {"/resume", nil}, {"/revoke", map[string]any{"confirm_name": "Base de Legal"}}} {
		m := o.e.json("POST", api1+"/connections/"+legal+step.path, o.carla, step.body, 200)
		if m["can_manage"] != true {
			t.Fatalf("%s answered can_manage=%v to the office's lead", step.path, m["can_manage"])
		}
	}
}

// The held sends of a private office's connections are its members' (beto, an admin outside it, does not see them),
// and the listing reads the connections once, not once per hold.
func TestConnectionHoldsFilterAndReadConnectionsOnce(t *testing.T) {
	gets := &atomic.Int32{}
	var gw *gateway.Gateway
	o := newOfficeConns(t, func(d *api.Deps) {
		d.Conns.Store = countingConns{Store: d.Conns.Store, gets: gets}
		ctl := controls.New(controls.NewMemStore(), nil, nil)
		gw = gateway.New(d.Conns, ctl, sanitize.NewSuspects(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		d.Controls, d.Gateway = ctl, gw
	})
	legal := o.create(o.ana, "Base de Legal", o.office, 201)
	shared := o.create(o.owner.AccessToken, "Base común", "", 201)
	orgID, now := o.owner.OrgID, time.Now().UTC()
	for i, conn := range []string{legal, legal, shared} {
		if err := gw.Conns.Store.PutHold(context.Background(), connections.Hold{ID: "h" + string(rune('1'+i)), OrgID: orgID, ConnectionID: conn,
			AgentID: "legal", Tool: "email", Action: "send", Status: connections.HoldHeld, HoldUntil: now.Add(time.Minute), CreatedAt: now,
			Recipients: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	holds := func(token string) []connections.Hold {
		t.Helper()
		var hs []connections.Hold
		if err := json.Unmarshal([]byte(o.body("GET", "/connection-holds", token, 200)), &hs); err != nil {
			t.Fatal(err)
		}
		return hs
	}
	gets.Store(0)
	if hs := holds(o.ana); len(hs) != 3 {
		t.Fatalf("a member of Legal sees %d holds", len(hs))
	}
	if n := gets.Load(); n > 1 {
		t.Fatalf("the listing read a connection %d times for 3 holds", n)
	}
	if hs := holds(o.beto); len(hs) != 1 || hs[0].ConnectionID != shared {
		t.Fatalf("an admin outside Legal sees %+v", hs)
	}
}

// When the agent's office cannot be told, granting an office's connection fails closed with its own answer, not as
// if the agent were of another office.
func TestConnectionGrantWhenTheOfficeCannotBeTold(t *testing.T) {
	var cs *connections.Service
	o := newOfficeConns(t, func(d *api.Deps) { cs = d.Conns })
	legal := o.create(o.ana, "Base de Legal", o.office, 201)
	cs.AgentOffice = func(context.Context, string, string) (string, error) { return "", errors.New("offices unavailable") }
	c, raw := o.code("PUT", "/connections/"+legal+"/grants/legal", o.ana, map[string]any{"capabilities": []string{"db.read"}})
	if c != 503 || strings.Contains(raw, connections.CodeAgentOtherOffice) || !strings.Contains(raw, "office_unavailable") {
		t.Fatalf("grant with the offices unavailable = %d %s", c, raw)
	}
}
