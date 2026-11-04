// Package database is the read-only database source (provider "database", docs/architecture/database-sources.md):
// agents granted a connection can list the tables they may read (db.schema) and run one read query (db.query).
// PostgreSQL first (Supabase, Neon, RDS, any Postgres); the configuration already names the engine so MySQL can be
// added later.
//
// Read-only is enforced by us, whatever the database user may do:
//  1. CheckSQL: one statement, only SELECT / WITH / EXPLAIN / VALUES / TABLE, no data-modifying or statement
//     keywords, no dangerous functions (sleep, dblink, files, large objects, set_config, sequences, SQL-as-text).
//  2. The extended protocol: the server itself refuses a second statement.
//  3. A READ ONLY transaction (verified with transaction_read_only) with statement_timeout,
//     idle_in_transaction_session_timeout and lock_timeout, ALWAYS rolled back, on a connection opened for this
//     call and closed after it. A write that slipped past (1) fails in the database with SQLSTATE 25006.
//
// Rows are capped (500) and bytes too (1 MiB); the reader stops the server when a cap is reached. The password
// is lent by the vault only while connecting and is scrubbed from every message.
package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/connections"
)

// MaxTables bounds a schema listing.
const MaxTables = 300

// Provider implements connections.Provider and connections.DatabaseExplorer.
type Provider struct {
	manifest     connections.Manifest
	allowPrivate bool
	// Limits of every call (DefaultLimits unless a test narrows them).
	Limits Limits
}

var (
	_ connections.Provider         = (*Provider)(nil)
	_ connections.DatabaseExplorer = (*Provider)(nil)
)

// New builds the provider. allowPrivate (DATABASE_ALLOW_PRIVATE, self-hosted on a trusted network) lets a source
// reach private addresses; it never relaxes the read-only rules.
func New(allowPrivate bool) (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	m, ok := ms[connections.DatabaseProvider]
	if !ok {
		return nil, fmt.Errorf("database: manifest missing")
	}
	return &Provider{manifest: m, allowPrivate: allowPrivate, Limits: DefaultLimits}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

// The password is pasted by an admin: there is no OAuth flow.
func (p *Provider) AuthStart(connections.AuthStartIn) (string, error) {
	return "", &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) AuthFinish(context.Context, connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	return connections.Bundle{}, connections.Identity{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) Refresh(_ context.Context, _ connections.OAuthApp, secret string) (connections.AccessToken, error) {
	return connections.StaticToken(secret)
}

// Revoke: the password is changed in the database; revoking the connection crypto-shreds our copy.
func (p *Provider) Revoke(context.Context, connections.OAuthApp, string) error { return nil }

// Test connects, opens the READ ONLY transaction (checking the server reports it) and rolls back.
func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	if h.Database == nil {
		return connections.TestResult{Code: CodeNotConfigured}
	}
	if h.Mode == connections.ModeSimulated {
		return connections.TestResult{OK: true, Account: h.Database.Account()}
	}
	start := time.Now()
	s, err := open(ctx, h, p.Limits, p.allowPrivate)
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	s.close()
	res.OK, res.Account = true, h.Database.Account()
	return res
}

func (p *Provider) Open(h connections.Handle) connections.Executor { return &executor{p: p, h: h} }

// Schema lists what the database user may read (admin screen and db.schema).
func (p *Provider) Schema(ctx context.Context, h connections.Handle, f connections.SchemaFilter) (connections.DBSchema, error) {
	if h.Database == nil {
		return connections.DBSchema{}, &connections.ProviderError{Code: CodeNotConfigured}
	}
	if h.Mode == connections.ModeSimulated {
		return simulatedSchema(h.Database.Schemas, f), nil
	}
	s, err := open(ctx, h, p.Limits, p.allowPrivate)
	if err != nil {
		return connections.DBSchema{}, err
	}
	defer s.close()
	return s.schema(ctx, h.Database.Schemas, f, MaxTables)
}

// Query runs one read statement under every rule of the package (admin test query and db.query).
func (p *Provider) Query(ctx context.Context, h connections.Handle, sql string) (connections.DBResult, error) {
	if h.Database == nil {
		return connections.DBResult{}, &connections.ProviderError{Code: CodeNotConfigured}
	}
	toks, clean, err := check(sql)
	if err != nil {
		return connections.DBResult{}, err
	}
	if h.Mode == connections.ModeSimulated {
		return simulatedRows(clean), nil
	}
	s, err := open(ctx, h, p.Limits, p.allowPrivate)
	if err != nil {
		return connections.DBResult{}, err
	}
	defer s.close()
	if err := s.checkSchemas(ctx, toks, h.Database.Schemas); err != nil {
		return connections.DBResult{}, err
	}
	if err := s.setSearchPath(ctx, h.Database.Schemas); err != nil {
		return connections.DBResult{}, classifyConnect(err)
	}
	return s.query(ctx, clean, p.Limits)
}

type executor struct {
	p *Provider
	h connections.Handle
}

// maxPartChars keeps every part under the gateway's 4000-character limit of an untrusted body.
const maxPartChars = 3800

func (x *executor) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	maxItems := c.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}
	switch strings.ToLower(c.Action) {
	case "schema":
		f := connections.SchemaFilter{Schema: connections.ArgStr(c.Args, "schema"), Table: connections.ArgStr(c.Args, "table")}
		sc, err := x.p.Schema(ctx, x.h, f)
		if err != nil {
			return connections.ExecResult{}, err
		}
		lines := make([]string, 0, len(sc.Tables))
		for _, t := range sc.Tables {
			lines = append(lines, describe(t))
		}
		if len(lines) == 0 {
			lines = append(lines, "(no hay tablas visibles para este usuario)")
		}
		items, shown := parts("schema", lines, maxItems)
		for i := range items {
			items[i].Meta["tables_total"] = len(sc.Tables)
			items[i].Meta["tables_truncated"] = sc.Truncated || shown < len(lines)
		}
		return connections.ExecResult{Items: items, Summary: fmt.Sprintf("%d table(s)", len(sc.Tables)), ResourceRef: "schema"}, nil
	case "query":
		sql := connections.ArgStr(c.Args, "sql", "query")
		res, err := x.p.Query(ctx, x.h, sql)
		ref := "query sha=" + shortHash(sql)
		if err != nil {
			return connections.ExecResult{ResourceRef: ref}, err
		}
		if res.Error != nil {
			it := connections.Item{ID: "query-error", Meta: map[string]any{"sqlstate": res.Error.SQLState},
				Texts: []connections.Text{{Name: "body", Value: "la consulta fallo (" + res.Error.SQLState + "): " + res.Error.Message +
					". Corrige la consulta (usa db.schema para ver tablas y columnas) y vuelve a pedirla."}}}
			return connections.ExecResult{Items: []connections.Item{it}, Summary: "query failed: " + res.Error.SQLState, ResourceRef: ref}, nil
		}
		cols := make([]string, len(res.Columns))
		for i, f := range res.Columns {
			cols[i] = f.Name + " (" + f.Type + ")"
		}
		lines := []string{"columnas: " + strings.Join(cols, ", ")}
		for _, r := range res.Rows {
			b, _ := json.Marshal(r)
			lines = append(lines, string(b))
		}
		items, shown := parts("rows", lines, maxItems)
		rowsShown := max(shown-1, 0)
		for i := range items {
			items[i].Meta["rows_returned"] = res.RowCount
			items[i].Meta["rows_shown"] = rowsShown
			items[i].Meta["truncated"] = res.Truncated || rowsShown < res.RowCount
			if res.Truncated {
				items[i].Meta["truncated_by"] = res.TruncatedBy
			}
		}
		var bytes int64
		for _, l := range lines {
			bytes += int64(len(l))
		}
		return connections.ExecResult{Items: items, BytesIn: bytes, ResourceRef: fmt.Sprintf("%s rows=%d", ref, res.RowCount),
			Summary: fmt.Sprintf("%d row(s)", res.RowCount)}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

// parts packs lines into at most max items of at most maxPartChars characters each. It returns the items and how
// many lines they hold.
func parts(prefix string, lines []string, max int) ([]connections.Item, int) {
	var out []connections.Item
	var b strings.Builder
	shown := 0
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, connections.Item{ID: fmt.Sprintf("%s-%d", prefix, len(out)+1), Meta: map[string]any{"part": len(out) + 1},
			Texts: []connections.Text{{Name: "body", Value: b.String()}}})
		b.Reset()
	}
	for _, l := range lines {
		l = connections.Truncate(l, maxPartChars)
		if b.Len() > 0 && b.Len()+1+len(l) > maxPartChars {
			flush()
		}
		if len(out) >= max {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
		shown++
	}
	if len(out) < max {
		flush()
	}
	return out, shown
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

// ---- simulated mode: an organization can try a source (and its grants) before pasting a password ----

func simulatedSchema(allowed []string, f connections.SchemaFilter) connections.DBSchema {
	all := []connections.DBTable{
		{Schema: "public", Name: "customers", Kind: "table", Comment: "simulated table", RowsEstimate: 3, Columns: []connections.DBColumn{
			{Name: "id", Type: "integer", NotNull: true}, {Name: "name", Type: "text", NotNull: true}, {Name: "city", Type: "text"}}},
		{Schema: "public", Name: "orders", Kind: "table", Comment: "simulated table", RowsEstimate: 3, Columns: []connections.DBColumn{
			{Name: "id", Type: "integer", NotNull: true}, {Name: "customer_id", Type: "integer"}, {Name: "total", Type: "numeric(10,2)"},
			{Name: "created_at", Type: "timestamp with time zone"}}},
	}
	out := connections.DBSchema{Tables: []connections.DBTable{}}
	for _, t := range all {
		if (len(allowed) > 0 && !contains(allowed, t.Schema)) || (f.Schema != "" && f.Schema != t.Schema) || (f.Table != "" && f.Table != t.Name) {
			continue
		}
		out.Tables = append(out.Tables, t)
	}
	return out
}

func simulatedRows(_ string) connections.DBResult {
	s := func(v string) *string { return &v }
	return connections.DBResult{Columns: []connections.DBField{{Name: "id", Type: "int4"}, {Name: "name", Type: "text"}, {Name: "note", Type: "text"}},
		Rows: [][]*string{{s("1"), s("Cliente simulado 1"), s("simulated row: connect the real database to see real data")},
			{s("2"), s("Cliente simulado 2"), nil}, {s("3"), s("Cliente simulado 3"), nil}}, RowCount: 3}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
