package database

// Integration tests against a real, DISPOSABLE Postgres. They run only when TEST_DATABASE_URL points to one, e.g.
//
//	docker run -d --name dbro-test-pg -e POSTGRES_PASSWORD=testpw -p 5440:5432 postgres:16.6-alpine
//	TEST_DATABASE_URL=postgres://postgres:testpw@localhost:5440/postgres?sslmode=disable go test ./internal/connections/database/
//
// Each test creates its own role (with every write privilege on its tables) and schemas, and drops them after.

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/connections"
)

type pgEnv struct {
	admin  *pgx.Conn
	role   string
	pass   string
	main   string // schema the source may see
	hidden string // schema it may not
	db     connections.Database
}

func newPG(t *testing.T) *pgEnv {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	port, _ := strconv.Atoi(u.Port())
	n := rand.Int63()
	e := &pgEnv{admin: admin, role: fmt.Sprintf("dbro_writer_%d", n), pass: fmt.Sprintf("Pw-%d-secret", n),
		main: fmt.Sprintf("dbro_main_%d", n), hidden: fmt.Sprintf("dbro_hidden_%d", n)}
	e.db = connections.Database{Engine: connections.EnginePostgres, Host: u.Hostname(), Port: port, Name: strings.TrimPrefix(u.Path, "/"),
		User: e.role, SSLMode: "disable"}
	e.exec(t, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, e.role, e.pass))
	e.exec(t, fmt.Sprintf(`GRANT CREATE ON DATABASE %s TO %s`, pgx.Identifier{e.db.Name}.Sanitize(), e.role))
	for _, sc := range []string{e.main, e.hidden} {
		e.exec(t, fmt.Sprintf(`CREATE SCHEMA %s AUTHORIZATION %s`, sc, e.role))
	}
	// Everything below is owned by the role: it may write all of it. Our transaction must not let it.
	e.exec(t, fmt.Sprintf(`SET ROLE %s`, e.role))
	e.exec(t, fmt.Sprintf(`CREATE TABLE %s.customers (id int PRIMARY KEY, name text NOT NULL, city text)`, e.main))
	e.exec(t, fmt.Sprintf(`COMMENT ON TABLE %s.customers IS 'Clientes activos'`, e.main))
	e.exec(t, fmt.Sprintf(`COMMENT ON COLUMN %s.customers.city IS 'Ciudad de facturacion'`, e.main))
	e.exec(t, fmt.Sprintf(`INSERT INTO %s.customers VALUES (1,'Ana','San Jose'),(2,'Luis',NULL),(3,'Marta','Cartago')`, e.main))
	e.exec(t, fmt.Sprintf(`CREATE TABLE %s.big AS SELECT g AS n, repeat('x', 100) AS pad FROM generate_series(1, 5000) g`, e.main))
	e.exec(t, fmt.Sprintf(`CREATE SEQUENCE %s.seq`, e.main))
	e.exec(t, fmt.Sprintf(`CREATE FUNCTION %s.evil() RETURNS int LANGUAGE sql VOLATILE AS 'INSERT INTO %s.customers VALUES (99, ''x'', NULL) RETURNING id'`, e.main, e.main))
	e.exec(t, fmt.Sprintf(`CREATE FUNCTION %s.slow() RETURNS int LANGUAGE sql AS 'SELECT 1 FROM pg_sleep(5)'`, e.main))
	e.exec(t, fmt.Sprintf(`CREATE TABLE %s.secret (v text)`, e.hidden))
	e.exec(t, fmt.Sprintf(`INSERT INTO %s.secret VALUES ('top secret')`, e.hidden))
	e.exec(t, `RESET ROLE`)
	t.Cleanup(func() {
		c := context.Background()
		for _, sc := range []string{e.main, e.hidden} {
			_, _ = admin.Exec(c, `DROP SCHEMA IF EXISTS `+sc+` CASCADE`)
		}
		_, _ = admin.Exec(c, fmt.Sprintf(`REVOKE ALL ON DATABASE %s FROM %s`, pgx.Identifier{e.db.Name}.Sanitize(), e.role))
		_, _ = admin.Exec(c, `DROP ROLE IF EXISTS `+e.role)
		_ = admin.Close(c)
	})
	return e
}

func (e *pgEnv) exec(t *testing.T, sql string) {
	t.Helper()
	if _, err := e.admin.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (e *pgEnv) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM `+e.main+`.customers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *pgEnv) handle(schemas ...string) connections.Handle {
	d := e.db
	d.Schemas = schemas
	pass := e.pass
	return connections.Handle{Org: "o1", ConnectionID: "c1", Mode: connections.ModeLive, Database: &d,
		Secret: func(_ context.Context, fn func([]byte) error) error { return fn([]byte(pass)) }}
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(true) // the disposable database listens on localhost
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The heart of the feature: the role OWNS the tables (it may insert, update, delete, create), and still nothing
// is written. The SQL check is bypassed on purpose here to prove the database-level lock on its own.
func TestWritesFailEvenWithWritePrivileges(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	before := e.count(t)
	writes := []string{
		`INSERT INTO ` + e.main + `.customers VALUES (10, 'x', NULL)`,
		`UPDATE ` + e.main + `.customers SET name = 'hacked'`,
		`DELETE FROM ` + e.main + `.customers`,
		`WITH x AS (INSERT INTO ` + e.main + `.customers VALUES (11, 'x', NULL) RETURNING id) SELECT * FROM x`,
		`SELECT ` + e.main + `.evil()`,
		`SELECT nextval('` + e.main + `.seq')`,
		`CREATE TABLE ` + e.main + `.created (a int)`,
		`TRUNCATE ` + e.main + `.customers`,
		`SELECT * FROM ` + e.main + `.customers FOR UPDATE`,
	}
	for _, w := range writes {
		s, err := open(context.Background(), e.handle(), p.Limits, true)
		if err != nil {
			t.Fatal(err)
		}
		_, qerr := s.query(context.Background(), w, p.Limits) // unchecked: straight to the database
		s.close()
		if code(qerr) != CodeSQLNotReadOnly {
			t.Errorf("%s: the database must refuse it as a write in a read-only transaction, got %v", w, qerr)
		}
	}
	// read-write cannot be switched back on from inside
	s, err := open(context.Background(), e.handle(), p.Limits, true)
	if err != nil {
		t.Fatal(err)
	}
	res, qerr := s.query(context.Background(), `SELECT set_config('transaction_read_only', 'off', true)`, p.Limits)
	s.close()
	if qerr == nil && res.Error == nil {
		t.Fatal("transaction_read_only was switched off")
	}
	// the checked path refuses before reaching the database, except for a function it cannot know: then the
	// database refuses
	if _, err := p.Query(context.Background(), e.handle(), `DELETE FROM `+e.main+`.customers`); code(err) != CodeSQLNotReadOnly {
		t.Fatalf("checked delete: %v", err)
	}
	if _, err := p.Query(context.Background(), e.handle(), `SELECT `+e.main+`.evil()`); code(err) != CodeSQLNotReadOnly {
		t.Fatalf("a writing function must fail in the database: %v", err)
	}
	if after := e.count(t); after != before {
		t.Fatalf("rows changed: %d -> %d", before, after)
	}
	var tables int
	_ = e.admin.QueryRow(context.Background(), `SELECT count(*) FROM pg_tables WHERE schemaname = $1 AND tablename = 'created'`, e.main).Scan(&tables)
	if tables != 0 {
		t.Fatal("a table was created")
	}
	var v int64
	_ = e.admin.QueryRow(context.Background(), `SELECT last_value FROM `+e.main+`.seq`).Scan(&v)
	if v != 1 {
		t.Fatalf("the sequence moved: %d", v)
	}
	// nothing was left open on the server
	var open int
	_ = e.admin.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE usename = $1 AND state <> 'idle'`, e.role).Scan(&open)
	if open != 0 {
		t.Fatalf("%d session(s) left open", open)
	}
}

func TestReadsWork(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	if res := p.Test(context.Background(), e.handle()); !res.OK || res.Account != e.role+"@"+e.db.Host+":"+strconv.Itoa(e.db.Port)+"/"+e.db.Name {
		t.Fatalf("test: %+v", res)
	}
	res, err := p.Query(context.Background(), e.handle(), `SELECT id, name, city FROM `+e.main+`.customers ORDER BY id;`)
	if err != nil || res.Error != nil {
		t.Fatalf("%v %+v", err, res.Error)
	}
	if res.RowCount != 3 || len(res.Columns) != 3 || res.Columns[0].Name != "id" || res.Columns[0].Type != "int4" {
		t.Fatalf("result %+v", res)
	}
	if *res.Rows[0][1] != "Ana" || res.Rows[1][2] != nil {
		t.Fatalf("values: %v %v (NULL must be nil)", *res.Rows[0][1], res.Rows[1][2])
	}
	if res, err := p.Query(context.Background(), e.handle(), `EXPLAIN SELECT * FROM `+e.main+`.customers`); err != nil || res.RowCount == 0 {
		t.Fatalf("explain: %v %+v", err, res)
	}
}

func TestRowAndByteCaps(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	start := time.Now()
	// 25 million rows streamed: the reader must stop the server at the cap, not read them all
	res, err := p.Query(context.Background(), e.handle(), `SELECT a.n, b.n FROM `+e.main+`.big a, `+e.main+`.big b`)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 500 || !res.Truncated || res.TruncatedBy != "rows" {
		t.Fatalf("rows cap: %d %v %s", res.RowCount, res.Truncated, res.TruncatedBy)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("stopping at the cap took %s", d)
	}
	p.Limits.MaxBytes = 2000
	res, err = p.Query(context.Background(), e.handle(), `SELECT pad FROM `+e.main+`.big`)
	if err != nil || !res.Truncated || res.TruncatedBy != "bytes" || res.RowCount > 25 {
		t.Fatalf("bytes cap: %v %d %v %s", err, res.RowCount, res.Truncated, res.TruncatedBy)
	}
}

func TestStatementTimeout(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	p.Limits.StatementTimeout = time.Second
	start := time.Now()
	// slow() sleeps 5 s inside the database: CheckSQL cannot know it; the statement_timeout stops it
	_, err := p.Query(context.Background(), e.handle(), `SELECT `+e.main+`.slow()`)
	if code(err) != CodeSQLTimeout {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("the timeout did not apply (%s)", d)
	}
}

func TestSchemaListsTablesColumnsAndComments(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	sc, err := p.Schema(context.Background(), e.handle(e.main), connections.SchemaFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var cust *connections.DBTable
	for i := range sc.Tables {
		if sc.Tables[i].Schema == e.hidden {
			t.Fatal("a schema outside the allowlist is listed")
		}
		if sc.Tables[i].Name == "customers" {
			cust = &sc.Tables[i]
		}
	}
	if cust == nil || cust.Comment != "Clientes activos" || len(cust.Columns) != 3 {
		t.Fatalf("customers: %+v", cust)
	}
	if c := cust.Columns[2]; c.Name != "city" || c.Comment != "Ciudad de facturacion" || c.NotNull {
		t.Fatalf("column: %+v", c)
	}
	if !cust.Columns[0].NotNull || cust.Columns[0].Type != "integer" {
		t.Fatalf("id: %+v", cust.Columns[0])
	}
	one, err := p.Schema(context.Background(), e.handle(), connections.SchemaFilter{Schema: e.hidden, Table: "secret"})
	if err != nil || len(one.Tables) != 1 {
		t.Fatalf("filter: %v %+v", err, one)
	}
}

func TestSchemaAllowlistOnQueries(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	h := e.handle(e.main)
	if _, err := p.Query(context.Background(), h, `SELECT * FROM `+e.hidden+`.secret`); code(err) != CodeSQLSchema {
		t.Fatalf("qualified name outside the allowlist: %v", err)
	}
	res, err := p.Query(context.Background(), h, `SELECT * FROM secret`)
	if err != nil || res.Error == nil || res.Error.SQLState != "42P01" {
		t.Fatalf("unqualified names resolve only in the allowed schemas: %v %+v", err, res.Error)
	}
	if res, err := p.Query(context.Background(), h, `SELECT count(*) FROM customers`); err != nil || res.Error != nil || *res.Rows[0][0] != "3" {
		t.Fatalf("search_path: %v %+v", err, res)
	}
}

func TestSQLErrorIsDataAndNeverCarriesThePassword(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	res, err := p.Query(context.Background(), e.handle(), `SELECT '`+e.pass+`'::int`)
	if err != nil || res.Error == nil || res.Error.SQLState != "22P02" {
		t.Fatalf("%v %+v", err, res.Error)
	}
	if strings.Contains(res.Error.Message, e.pass) || !strings.Contains(res.Error.Message, "[REDACTED]") {
		t.Fatalf("the password reached the message: %q", res.Error.Message)
	}
	res, err = p.Query(context.Background(), e.handle(), `SELECT '`+e.pass+`' AS v`)
	if err != nil || strings.Contains(*res.Rows[0][0], e.pass) {
		t.Fatalf("the password reached a value: %v", err)
	}
	// a wrong password is "re-authorize", without any driver text
	h := e.handle()
	h.Secret = func(_ context.Context, fn func([]byte) error) error { return fn([]byte("wrong-password")) }
	if r := p.Test(context.Background(), h); r.OK || r.Code != connections.CodeReauthRequired {
		t.Fatalf("wrong password: %+v", r)
	}
}

// Without DATABASE_ALLOW_PRIVATE an admin-written host cannot reach the server's own network.
func TestPrivateAddressesAreRefusedByDefault(t *testing.T) {
	e := newPG(t)
	p, _ := New(false)
	for _, host := range []string{"127.0.0.1", "localhost"} {
		h := e.handle()
		h.Database.Host = host
		if r := p.Test(context.Background(), h); r.OK || r.Code != CodeAddressNotAllowed {
			t.Fatalf("%s: %+v", host, r)
		}
	}
}

func TestExecutorPartsAndErrorsForAgents(t *testing.T) {
	e := newPG(t)
	p := testProvider(t)
	ex := p.Open(e.handle())
	out, err := ex.Execute(context.Background(), connections.ExecCall{Tool: "db", Action: "query", MaxItems: 3,
		Args: map[string]any{"sql": `SELECT n, pad FROM ` + e.main + `.big ORDER BY n`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 3 || out.Items[0].Meta["rows_returned"] != 500 || out.Items[0].Meta["truncated"] != true {
		t.Fatalf("parts: %d %+v", len(out.Items), out.Items[0].Meta)
	}
	for _, it := range out.Items {
		if len(it.Texts[0].Value) > maxPartChars {
			t.Fatalf("part too long: %d", len(it.Texts[0].Value))
		}
	}
	if !strings.HasPrefix(out.Items[0].Texts[0].Value, "columnas: n (int4), pad (text)") {
		t.Fatalf("header: %.60s", out.Items[0].Texts[0].Value)
	}
	out, err = ex.Execute(context.Background(), connections.ExecCall{Tool: "db", Action: "query", Args: map[string]any{"sql": "SELECT * FROM nope"}})
	if err != nil || len(out.Items) != 1 || out.Items[0].Meta["sqlstate"] != "42P01" {
		t.Fatalf("a fixable error is data for the agent: %v %+v", err, out.Items)
	}
	out, err = ex.Execute(context.Background(), connections.ExecCall{Tool: "db", Action: "schema", MaxItems: 5})
	if err != nil || len(out.Items) == 0 || !strings.Contains(out.Items[0].Texts[0].Value, "customers") {
		t.Fatalf("schema: %v %+v", err, out.Items)
	}
}
