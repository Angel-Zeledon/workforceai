package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
)

// D3: a connection may belong to a department (office). Only the agents of that office may hold a grant on it, and
// the gateway refuses (as if there were no grant) an old grant that no longer fits: the agent moved to another office,
// or its office cannot be told. A connection without an office works as always.

// officeOf answers the office of each agent ("" = General); an agent in fail answers an error.
type officeOf struct {
	of   map[string]string
	fail map[string]bool
}

func (o *officeOf) fn(_ context.Context, _, agent string) (string, error) {
	if o.fail[agent] {
		return "", errors.New("offices unavailable")
	}
	return o.of[agent], nil
}

func officeDB(t *testing.T, office string) (*connectors, connections.Connection, *officeOf) {
	t.Helper()
	c := newDBEnv(t)
	who := &officeOf{of: map[string]string{"assistant": "ventas", "sales": ""}, fail: map[string]bool{}}
	c.cs.AgentOffice = who.fn
	d := connections.Database{Host: "db.example.test", User: "reader"}
	conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: connections.DatabaseProvider, Kind: "api_key",
		Label: "Ventas", Mode: connections.ModeSimulated, Database: &d, Actor: "u1", OfficeID: office})
	if err != nil {
		t.Fatal(err)
	}
	if conn.OfficeID != office {
		t.Fatalf("office_id = %q, want %q", conn.OfficeID, office)
	}
	return c, conn, who
}

func TestOfficeConnectionGrantsOnlyItsOfficeAgents(t *testing.T) {
	c, conn, _ := officeDB(t, "ventas")
	ctx := context.Background()
	_, err := c.cs.PutGrant(ctx, org, conn.ID, "sales", connections.GrantInput{Capabilities: []string{"db.read"}}, "admin1")
	if !errors.Is(err, connections.ErrInvalid) || !strings.Contains(err.Error(), connections.CodeAgentOtherOffice) {
		t.Fatalf("an agent of another office cannot be granted: %v", err)
	}
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Status != "succeeded" {
		t.Fatalf("the office's own agent uses it: %+v", out)
	}
	if refs := c.g.SourcesFor(ctx, org, "assistant"); len(refs) != 1 {
		t.Fatalf("sources: %+v", refs)
	}
}

func TestOfficeConnectionOldGrantIsRefused(t *testing.T) {
	c, conn, who := officeDB(t, "ventas")
	ctx := context.Background()
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	tasks := []application.PlanTaskInfo{{ID: "t1", Title: "Consulta", AgentID: "assistant"}}

	who.of["assistant"] = "compras" // the agent moved to another office: its old grant no longer fits
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Decision != "denied" || out.DenyReason != connections.CodeScopeNotGranted {
		t.Fatalf("an old grant of another office is refused as no grant: %+v", out)
	}
	if refs := c.g.SourcesFor(ctx, org, "assistant"); len(refs) != 0 {
		t.Fatalf("no source listed: %+v", refs)
	}
	if pf, _, _, err := c.g.Begin(ctx, org, "r-moved", tasks); err != nil || len(pf.ReachableConnections) != 0 {
		t.Fatalf("not reachable in the plan: %v %+v", err, pf.ReachableConnections)
	}

	who.of["assistant"], who.fail["assistant"] = "ventas", true // its office cannot be told: fail closed
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Decision != "denied" || out.DenyReason != connections.CodeScopeNotGranted {
		t.Fatalf("unknown office fails closed: %+v", out)
	}
	if refs := c.g.SourcesFor(ctx, org, "assistant"); len(refs) != 0 {
		t.Fatalf("no source listed: %+v", refs)
	}

	who.fail["assistant"] = false
	c.cs.AgentOffice = nil // no offices wired at all: an office connection serves nobody
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.DenyReason != connections.CodeScopeNotGranted {
		t.Fatalf("without the offices an office connection fails closed: %+v", out)
	}
}

func TestConnectionWithoutOfficeIsUnchanged(t *testing.T) {
	c, conn, who := officeDB(t, "")
	who.fail["assistant"] = true // never asked: the connection has no office
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	c.grant(conn.ID, "sales", connections.GrantInput{Capabilities: []string{"db.read"}})
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Status != "succeeded" {
		t.Fatalf("a connection without office: %+v", out)
	}
	c.cs.AgentOffice = nil
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Status != "succeeded" {
		t.Fatalf("a connection without office needs no offices: %+v", out)
	}
}

func TestConnectionOfficeIsChangedByPatch(t *testing.T) {
	c, conn, _ := officeDB(t, "")
	ctx := context.Background()
	office := "ventas"
	got, err := c.cs.Patch(ctx, org, conn.ID, connections.PatchInput{OfficeID: &office, Actor: "u1"})
	if err != nil || got.OfficeID != "ventas" {
		t.Fatalf("patch: %v %+v", err, got.OfficeID)
	}
	general := "general" // General is "no office"
	if got, err = c.cs.Patch(ctx, org, conn.ID, connections.PatchInput{OfficeID: &general, Actor: "u1"}); err != nil || got.OfficeID != "" {
		t.Fatalf("general: %v %q", err, got.OfficeID)
	}
}
