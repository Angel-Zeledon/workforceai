package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/database"
)

// A read-only database connection created from a pasted connection string: the server splits it, the password
// goes to the vault and comes back nowhere (no hint either); the admin screens list tables and run a test query
// under the agents' rules.
func TestDatabaseConnectionFromConnectionString(t *testing.T) {
	e := newConnEnv(t, false, true)
	p, err := database.New(false)
	if err != nil {
		t.Fatal(err)
	}
	e.conns.Providers[connections.DatabaseProvider] = p
	const pw = "Sup3r-Secret-DB-Passw0rd"
	resp, raw := e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "database", "kind": "api_key", "label": "Ventas",
		"connection_string": "postgresql://reader.ref:" + pw + "@db.example.test:6543/app?sslmode=verify-full",
		"database":          map[string]any{"description": "Pedidos", "schemas": []string{"public"}}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), pw) || strings.Contains(string(raw), "connection_string") {
		t.Fatalf("the response carries the secret: %s", raw)
	}
	var c struct {
		ID           string                `json:"id"`
		AccountLabel string                `json:"account_label"`
		Database     *connections.Database `json:"database"`
		Credential   *struct {
			Hint string `json:"hint"`
		} `json:"credential"`
	}
	_ = json.Unmarshal(raw, &c)
	if c.Database == nil || c.Database.Host != "db.example.test" || c.Database.Port != 6543 || c.Database.User != "reader.ref" ||
		c.Database.SSLMode != "verify-full" || c.Database.Description != "Pedidos" || len(c.Database.Schemas) != 1 {
		t.Fatalf("database: %+v", c.Database)
	}
	if c.AccountLabel != "reader.ref@db.example.test:6543/app" || c.Credential == nil || c.Credential.Hint != "" {
		t.Fatalf("account %q credential %+v", c.AccountLabel, c.Credential)
	}
	// GET and the list never carry it either
	for _, path := range []string{"/api/v1/connections/" + c.ID, "/api/v1/connections"} {
		if _, raw := e.do("GET", path, "", nil, nil); strings.Contains(string(raw), pw) {
			t.Fatalf("%s carries the password", path)
		}
	}
	// the host is public-looking but does not resolve: a stable code, no driver text
	resp, raw = e.do("POST", "/api/v1/connections/"+c.ID+"/database/query", "", map[string]any{"sql": "SELECT 1"}, nil)
	if resp.StatusCode != 422 || strings.Contains(string(raw), "example.test") || strings.Contains(string(raw), pw) {
		t.Fatalf("query on an unreachable host = %d %s", resp.StatusCode, raw)
	}
	// a write is refused before anything is dialled
	resp, raw = e.do("POST", "/api/v1/connections/"+c.ID+"/database/query", "", map[string]any{"sql": "DELETE FROM t"}, nil)
	if resp.StatusCode != 422 || !strings.Contains(string(raw), "sql_not_read_only") {
		t.Fatalf("write = %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(e.logs.String(), pw) {
		t.Fatal("the password reached the logs")
	}
	// a connection string is only for database connections
	resp, _ = e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "custom_api", "kind": "api_key", "label": "x",
		"connection_string": "postgresql://u:p@h/db"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("connection_string on custom_api = %d", resp.StatusCode)
	}
}

func TestDatabaseSimulatedConnectionShowsSampleTables(t *testing.T) {
	e := newConnEnv(t, false, true)
	p, _ := database.New(false)
	e.conns.Providers[connections.DatabaseProvider] = p
	resp, raw := e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "database", "kind": "api_key", "label": "Demo",
		"mode": "simulated", "database": map[string]any{"host": "db.example.test", "user": "reader"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %s", resp.StatusCode, raw)
	}
	var c struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &c)
	resp, raw = e.do("GET", "/api/v1/connections/"+c.ID+"/database/schema", "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"customers"`) {
		t.Fatalf("schema = %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("POST", "/api/v1/connections/"+c.ID+"/database/query", "", map[string]any{"sql": "SELECT * FROM customers"}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"row_count":3`) {
		t.Fatalf("query = %d %s", resp.StatusCode, raw)
	}
	// MySQL is announced, not available
	resp, raw = e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "database", "kind": "api_key", "label": "My",
		"mode": "simulated", "database": map[string]any{"engine": "mysql", "host": "db.example.test", "user": "reader"}}, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "engine_not_supported") {
		t.Fatalf("mysql = %d %s", resp.StatusCode, raw)
	}
}
