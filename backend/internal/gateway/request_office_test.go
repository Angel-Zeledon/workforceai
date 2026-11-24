package gateway

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
)

// A call that reaches the gateway without the office of its request in its context (an action approved after the work
// was done, from the outbox or the approvals screen) gets it from its request: the connection of the office the agent
// works for in that request is usable, the one of its base office is not, and a request whose office cannot be told is
// denied, never run with the agent's base office.
func TestExecuteRestoresTheOfficeOfTheRequest(t *testing.T) {
	c := newDBEnv(t)
	// the assistant's base office is ventas and it also works for legal: in a request of legal the office is legal
	c.cs.AgentOffice = func(ctx context.Context, _, _ string) (string, error) {
		if o := application.RequestOfficeFrom(ctx); o != "" {
			return o, nil
		}
		return "ventas", nil
	}
	mk := func(label, office string) connections.Connection {
		d := connections.Database{Host: "db.example.test", User: "reader"}
		conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: connections.DatabaseProvider, Kind: "api_key",
			Label: label, Mode: connections.ModeSimulated, Database: &d, Actor: "u1", OfficeID: office})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.cs.PutGrant(application.WithRequestOffice(context.Background(), office), org, conn.ID, "assistant",
			connections.GrantInput{Capabilities: []string{"db.read"}}, "admin1"); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	legalConn, ventasConn := mk("Legal", "legal"), mk("Ventas", "ventas")
	offices := map[string]string{"req-legal": "legal", "req-base": "ventas"}
	c.g.RequestOffice = func(_ context.Context, _, id string) (string, error) {
		if id == "req-broken" {
			return "", errors.New("store unavailable")
		}
		return offices[id], nil
	}
	sees := func(ctx context.Context, requestID string) int {
		t.Helper()
		return len(c.g.SourcesFor(ctx, org, "assistant")) // the connections the agent may use in that ctx
	}
	legalCtx := application.WithRequestOffice(context.Background(), "legal")
	if n := sees(legalCtx, ""); n != 1 {
		t.Fatalf("in a request of legal the agent has %d sources, want the legal one only", n)
	}
	// the call with no office in its context: restored from its request, and still executes
	cl := call("db", "query", map[string]any{"sql": "SELECT 1"})
	cl.RequestID = "req-legal"
	if out := c.g.Execute(context.Background(), cl); out.Status != "succeeded" || out.ConnectionID != legalConn.ID {
		t.Fatalf("an approved call of a legal request must use legal's connection (%s): %+v", legalConn.ID, out)
	}
	cl.RequestID = "req-base"
	if out := c.g.Execute(context.Background(), cl); out.Status != "succeeded" || out.ConnectionID != ventasConn.ID {
		t.Fatalf("a call of a request of the base office must use ventas' connection (%s): %+v", ventasConn.ID, out)
	}
	// an unreadable request: denied, not run with the base office
	cl.RequestID = "req-broken"
	if out := c.g.Execute(context.Background(), cl); out.Decision != "denied" || out.DenyReason != "office_scope_unknown" {
		t.Fatalf("a request whose office cannot be told: %+v", out)
	}
	// a context that already carries its office is left alone (the hook is not asked)
	asked := false
	c.g.RequestOffice = func(context.Context, string, string) (string, error) { asked = true; return "ventas", nil }
	cl.RequestID = "req-legal"
	if out := c.g.Execute(legalCtx, cl); out.Status != "succeeded" || asked {
		t.Fatalf("a ctx with its office: %+v (asked %v)", out, asked)
	}
}
