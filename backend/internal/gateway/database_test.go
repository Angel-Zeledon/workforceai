package gateway

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/database"
	"aiworkforce/backend/internal/vault"
)

// Read-only database sources go through the same gateway as every connector: grants decide who may query, every
// call is counted and audited, what comes back is delimited untrusted data and the password never leaves the vault.

func newDBEnv(t *testing.T) *connectors {
	c := newConnectors(t)
	p, err := database.New(true) // the disposable test database is on localhost
	if err != nil {
		t.Fatal(err)
	}
	c.cs.Providers[connections.DatabaseProvider] = p
	return c
}

func (c *connectors) dbSim(label string, d connections.Database) connections.Connection {
	c.t.Helper()
	conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: connections.DatabaseProvider, Kind: "api_key",
		Label: label, Mode: connections.ModeSimulated, Database: &d, Actor: "u1"})
	if err != nil {
		c.t.Fatal(err)
	}
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	return conn
}

func TestDatabaseReadIsUntrustedDataAndCounted(t *testing.T) {
	c := newDBEnv(t)
	cn := c.dbSim("Ventas", connections.Database{Host: "db.example.test", User: "reader", Description: "Pedidos y clientes"})
	out := c.exec(call("db", "query", map[string]any{"sql": "SELECT * FROM customers LIMIT 3"}))
	readsAreUntrustedData(t, out, false)
	if !strings.Contains(out.Blocks[0], "Cliente simulado 1") || !strings.Contains(out.Blocks[0], "rows_returned: 3") {
		t.Fatalf("block: %s", out.Blocks[0])
	}
	out = c.exec(call("db", "schema", nil))
	readsAreUntrustedData(t, out, false)
	us, _ := c.cs.Store.ListUsage(context.Background(), org, connections.UsageFilter{ConnectionID: cn.ID})
	if len(us) != 2 {
		t.Fatalf("usage rows = %d", len(us))
	}
	refs := c.g.SourcesFor(context.Background(), org, "assistant")
	if len(refs) != 1 || refs[0].Engine != "postgres" || refs[0].Description != "Pedidos y clientes" || strings.Join(refs[0].Tools, ",") != "db.query,db.schema" {
		t.Fatalf("sources: %+v", refs)
	}
}

func TestDatabaseWriteIsRefusedWithAStableCode(t *testing.T) {
	c := newDBEnv(t)
	c.dbSim("Ventas", connections.Database{Host: "db.example.test", User: "reader"})
	for sql, want := range map[string]string{
		"DELETE FROM customers":                 database.CodeSQLNotReadOnly,
		"SELECT 1; DROP TABLE customers":        database.CodeSQLMultiple,
		"SELECT pg_sleep(100)":                  database.CodeSQLForbiddenFunc,
		"WITH x AS (UPDATE t SET a=1) SELECT 1": database.CodeSQLNotReadOnly,
	} {
		out := c.exec(call("db", "query", map[string]any{"sql": sql}))
		if out.Status != "failed" || out.DenyReason != want || len(out.Blocks) != 0 {
			t.Errorf("%q: %+v", sql, out)
		}
	}
	if c.g.IsSideEffect("db", "query") || c.g.IsSideEffect("db", "schema") {
		t.Fatal("database tools are reads")
	}
}

func TestDatabaseNotGrantedOrAmbiguousIsDenied(t *testing.T) {
	c := newDBEnv(t)
	d := connections.Database{Host: "db.example.test", User: "reader"}
	conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: connections.DatabaseProvider, Kind: "api_key",
		Label: "Sin conceder", Mode: connections.ModeSimulated, Database: &d, Actor: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.Decision != "denied" || out.DenyReason != connections.CodeScopeNotGranted {
		t.Fatalf("connected is not granted: %+v", out)
	}
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	c.dbSim("Otra", d)
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1"})); out.DenyReason != CodeSourceAliasRequired {
		t.Fatalf("two databases need an alias: %+v", out)
	}
	if out := c.exec(call("db", "query", map[string]any{"sql": "SELECT 1", "connection_alias": "otra"})); out.Status != "succeeded" {
		t.Fatalf("with alias: %+v", out)
	}
}

// End to end against the disposable Postgres: the password is stored in the vault, lent to the adapter for the
// call, and is in no block, summary, usage row or audit entry, even when the query echoes it.
func TestDatabaseLiveQueryNeverLeaksThePassword(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, _ := url.Parse(raw)
	pass, _ := u.User.Password()
	port, _ := strconv.Atoi(u.Port())
	c := newDBEnv(t)
	d := connections.Database{Host: u.Hostname(), Port: port, Name: strings.TrimPrefix(u.Path, "/"), User: u.User.Username(), SSLMode: "disable"}
	sec := vault.SecretFromString(pass)
	conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: connections.DatabaseProvider, Kind: "api_key",
		Label: "Real", Database: &d, Secret: &sec, Actor: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if conn.Credential == nil || conn.Credential.Hint != "" || conn.AccountLabel != d.User+"@"+d.Host+":"+u.Port()+"/"+d.Name {
		t.Fatalf("view: %+v %+v", conn.Credential, conn.AccountLabel)
	}
	if res, err := c.cs.Test(context.Background(), org, conn.ID, "u1"); err != nil || !res.OK {
		t.Fatalf("test: %+v %v", res, err)
	}
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: []string{"db.read"}})
	out := c.exec(call("db", "query", map[string]any{"sql": "SELECT '" + pass + "' AS echo, current_setting('transaction_read_only') AS ro"}))
	readsAreUntrustedData(t, out, false)
	all := strings.Join(out.Blocks, "\n") + out.Summary + c.sink.text()
	if strings.Contains(all, pass) {
		t.Fatal("the password reached the agent, the audit or the events")
	}
	if !strings.Contains(out.Blocks[0], `"on"`) {
		t.Fatalf("the agent's query must run in a read-only transaction: %s", out.Blocks[0])
	}
	out = c.exec(call("db", "query", map[string]any{"sql": "SELECT * FROM table_that_does_not_exist"}))
	if out.Status != "succeeded" || !strings.Contains(out.Blocks[0], "42P01") {
		t.Fatalf("a fixable SQL error comes back as data: %+v", out)
	}
}
