// Package tenantdata implements the data lifecycle of a tenant (GDPR): the
// export of everything an organization stored (GET /org/export), the scheduled
// deletion with a grace period (/org/deletion) and the purge that a background
// janitor runs once the grace period is over (also for expired trial accounts).
//
// Nothing here hard-codes the list of tenant tables: the tables are discovered
// from the live catalog (every table with an org_id column, plus the tables
// that reach one of those through foreign keys) and the purge order is a
// topological sort of the foreign-key graph. New migrations are covered
// automatically; the guard tests fail when a new table cannot be classified.
// See README.md in this directory.
package tenantdata

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Well-known tables of the identity model.
const (
	OrgTable        = "organizations" // the tenant itself (org_id is a generated copy of id)
	MembershipTable = "memberships"   // removed after the data, before the users
	AccountTable    = "users"         // global accounts: deleted only when no membership is left
	orgColumn       = "org_id"
)

// GlobalTables are not tenant data and are never exported nor purged.
// A table that is neither tenant data nor listed here makes the guard test fail.
var GlobalTables = map[string]string{
	"schema_migrations": "migration ledger",
	"org_tombstones":    "non-personal record of purged organizations (survives the purge on purpose)",
}

// ExcludedFromExport are tenant tables that are purged but never exported
// because every useful column is a secret or a one-time handshake value.
var ExcludedFromExport = map[string]string{
	"oauth_states": "in-flight OAuth handshakes: hashed state and sealed PKCE verifier only",
}

// RedactedColumns is the explicit denylist ("table.column") of secrets that
// are never exported. secretColumn also applies the name patterns below, and
// every bytea column is redacted unless it is listed in BlobColumns.
var RedactedColumns = map[string]string{
	"users.password_hash":                 "argon2id password hash",
	"refresh_tokens.token_hash":           "session token hash",
	"invitations.token_hash":              "invitation token hash",
	"email_verifications.token_hash":      "verification token hash",
	"oauth_states.state_hash":             "OAuth state hash",
	"oauth_states.pkce_verifier_enc":      "sealed PKCE verifier",
	"credentials.ciphertext":              "vault ciphertext of a connection credential",
	"credentials.nonce":                   "vault nonce",
	"org_data_keys.wrapped_dek":           "wrapped data-encryption key of the vault",
	"connections.oauth_client_secret_enc": "sealed OAuth client secret",
	"org_llm_credentials.sealed_key":      "sealed bring-your-own-model API key",
	"push_subscriptions.endpoint":         "Web Push capability URL",
	"push_subscriptions.p256dh":           "Web Push encryption key",
	"push_subscriptions.auth":             "Web Push auth secret",
}

// BlobColumns are the bytea columns that hold the organization's own file
// bytes (Postgres object-store fallback). They are exported as separate files
// inside the ZIP. Every other bytea column is treated as a secret.
var BlobColumns = map[string]bool{
	"drive_objects.data":  true,
	"artifact_blobs.data": true,
}

// secretName matches column names that hold credentials, whatever the table.
var secretName = regexp.MustCompile(`(?i)(password|passwd|secret|ciphertext|sealed|wrapped|private_key|api_key|pkce|(^|_)nonce$|(^|_)dek($|_)|_enc$|(^|_)(password|token|state|key|secret|code)_hash$|(^|_)(access|refresh|id|bearer)_token$)`)

// secretColumn reports whether a column must never leave the database.
func secretColumn(table string, c Column) bool {
	if _, ok := RedactedColumns[table+"."+c.Name]; ok {
		return true
	}
	if secretName.MatchString(c.Name) {
		return true
	}
	return c.Type == "bytea" && !BlobColumns[table+"."+c.Name]
}

// Column is one column of a table.
type Column struct {
	Name string
	Type string // format_type, e.g. "text", "bytea", "timestamp with time zone"
}

// FK is a foreign key: Child(ChildCols) REFERENCES Parent(ParentCols).
type FK struct {
	Child, Parent         string
	ChildCols, ParentCols []string
}

// Kind classifies a table.
type Kind int

const (
	KindUnclassified Kind = iota
	KindOrg               // has org_id
	KindChild             // no org_id, reaches an org table through foreign keys
	KindAccount           // users and the tables that only hang from users
	KindGlobal            // GlobalTables
)

func (k Kind) String() string {
	return [...]string{"unclassified", "org", "child", "account", "global"}[k]
}

// Table is a discovered table.
type Table struct {
	Name    string
	Columns []Column
	Kind    Kind
	// Via are the foreign keys through which a KindChild (or KindAccount) table reaches its scope.
	Via []FK
}

func (t *Table) has(col string) bool {
	for _, c := range t.Columns {
		if c.Name == col {
			return true
		}
	}
	return false
}

// Schema is the classified catalog of the current schema.
type Schema struct {
	Tables map[string]*Table
	FKs    []FK
}

// Querier is what discovery needs (pgx.Tx, *pgx.Conn and *pgxpool.Pool all fit).
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Discover reads the tables, columns and foreign keys of the current schema
// (search_path) from pg_catalog, which shows every table whatever the
// privileges of the role, and classifies them.
func Discover(ctx context.Context, q Querier) (*Schema, error) {
	s := &Schema{Tables: map[string]*Table{}}
	rows, err := q.Query(ctx, `
		SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY c.relname, a.attnum`)
	if err != nil {
		return nil, fmt.Errorf("tenantdata: discover columns: %w", err)
	}
	for rows.Next() {
		var tbl, col, typ string
		if err := rows.Scan(&tbl, &col, &typ); err != nil {
			rows.Close()
			return nil, err
		}
		t := s.Tables[tbl]
		if t == nil {
			t = &Table{Name: tbl}
			s.Tables[tbl] = t
		}
		t.Columns = append(t.Columns, Column{Name: col, Type: typ})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(s.Tables) == 0 {
		return nil, fmt.Errorf("tenantdata: no tables in the current schema")
	}
	rows, err = q.Query(ctx, `
		SELECT cl.relname, pl.relname,
		       ARRAY(SELECT a.attname FROM unnest(con.conkey) WITH ORDINALITY k(n, i)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.n ORDER BY k.i)::text[],
		       ARRAY(SELECT a.attname FROM unnest(con.confkey) WITH ORDINALITY k(n, i)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.n ORDER BY k.i)::text[]
		FROM pg_constraint con
		JOIN pg_class cl ON cl.oid = con.conrelid
		JOIN pg_class pl ON pl.oid = con.confrelid
		WHERE con.contype = 'f'
		  AND cl.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
		  AND pl.relnamespace = cl.relnamespace
		ORDER BY cl.relname, con.conname`)
	if err != nil {
		return nil, fmt.Errorf("tenantdata: discover foreign keys: %w", err)
	}
	for rows.Next() {
		var fk FK
		if err := rows.Scan(&fk.Child, &fk.Parent, &fk.ChildCols, &fk.ParentCols); err != nil {
			rows.Close()
			return nil, err
		}
		s.FKs = append(s.FKs, fk)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.classify()
	return s, nil
}

// NewSchema builds a classified schema from explicit tables and foreign keys (tests).
func NewSchema(tables map[string][]Column, fks []FK) *Schema {
	s := &Schema{Tables: map[string]*Table{}, FKs: fks}
	for n, cols := range tables {
		s.Tables[n] = &Table{Name: n, Columns: cols}
	}
	s.classify()
	return s
}

func (s *Schema) classify() {
	for _, t := range s.Tables {
		switch {
		case GlobalTables[t.Name] != "":
			t.Kind = KindGlobal
		case t.has(orgColumn):
			t.Kind = KindOrg
		case t.Name == AccountTable:
			t.Kind = KindAccount
		}
	}
	// Fixpoint: a table without org_id that references a scoped table is a child
	// of the organization; one that only references account tables is an account table.
	for changed := true; changed; {
		changed = false
		for _, name := range s.names() {
			t := s.Tables[name]
			if t.Kind != KindUnclassified {
				continue
			}
			var org, acct []FK
			for _, fk := range s.FKs {
				if fk.Child != name || fk.Parent == name {
					continue
				}
				switch s.Tables[fk.Parent].Kind {
				case KindOrg, KindChild:
					org = append(org, fk)
				case KindAccount:
					acct = append(acct, fk)
				}
			}
			switch {
			case len(org) > 0:
				t.Kind, t.Via, changed = KindChild, org, true
			case len(acct) > 0:
				t.Kind, t.Via, changed = KindAccount, acct, true
			}
		}
	}
}

func (s *Schema) names() []string {
	out := make([]string, 0, len(s.Tables))
	for n := range s.Tables {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// OfKind lists the tables of the given kinds, sorted by name.
func (s *Schema) OfKind(kinds ...Kind) []string {
	var out []string
	for _, n := range s.names() {
		for _, k := range kinds {
			if s.Tables[n].Kind == k {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// Unclassified lists the tables that are neither tenant data, account data nor
// declared global: the guard test requires this to be empty.
func (s *Schema) Unclassified() []string { return s.OfKind(KindUnclassified) }

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

// OrgScope returns a SQL predicate on alias that is true for the rows of table
// that belong to the organization bound to $1. ok is false for tables that are
// not organization data (account and global tables).
func (s *Schema) OrgScope(table, alias string) (string, bool) {
	return s.orgScope(table, alias, 0)
}

func (s *Schema) orgScope(table, alias string, depth int) (string, bool) {
	t := s.Tables[table]
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case KindOrg:
		return alias + "." + ident(orgColumn) + " = $1", true
	case KindChild:
		return s.via(t.Via, alias, depth, s.orgScope)
	}
	return "", false
}

// via ORs, over the given foreign keys, "the referenced parent row is in scope".
func (s *Schema) via(fks []FK, alias string, depth int, parentScope func(table, alias string, depth int) (string, bool)) (string, bool) {
	if depth > 16 {
		return "", false
	}
	var ors []string
	for i, fk := range fks {
		pa := fmt.Sprintf("p%d_%d", depth, i)
		inner, ok := parentScope(fk.Parent, pa, depth+1)
		if !ok {
			continue
		}
		var on []string
		for j := range fk.ChildCols {
			on = append(on, pa+"."+ident(fk.ParentCols[j])+" = "+alias+"."+ident(fk.ChildCols[j]))
		}
		ors = append(ors, "EXISTS (SELECT 1 FROM "+ident(fk.Parent)+" "+pa+" WHERE "+strings.Join(on, " AND ")+" AND "+inner+")")
	}
	if len(ors) == 0 {
		return "", false
	}
	return "(" + strings.Join(ors, " OR ") + ")", true
}

// UserDependents returns the tables whose rows hang from users (directly or
// through other such tables) in a safe DELETE order, each with a predicate on
// alias t selecting the rows of the accounts listed in $1 (text[]). Deleting
// these, then the users, removes an account completely (refresh tokens,
// e-mail verifications, any future per-user table).
func (s *Schema) UserDependents() ([]ScopedTable, error) {
	dep := map[string]bool{AccountTable: true}
	for changed := true; changed; {
		changed = false
		for _, fk := range s.FKs {
			if dep[fk.Parent] && !dep[fk.Child] && s.Tables[fk.Child] != nil {
				dep[fk.Child], changed = true, true
			}
		}
	}
	delete(dep, AccountTable)
	order, err := topo(dep, s.FKs, "")
	if err != nil {
		return nil, err
	}
	var scope func(table, alias string, depth int) (string, bool)
	scope = func(table, alias string, depth int) (string, bool) {
		if table == AccountTable {
			return alias + ".id = ANY($1::text[])", true
		}
		if !dep[table] {
			return "", false
		}
		var fks []FK
		for _, fk := range s.FKs {
			if fk.Child == table && fk.Parent != table && (dep[fk.Parent] || fk.Parent == AccountTable) {
				fks = append(fks, fk)
			}
		}
		return s.via(fks, alias, depth, scope)
	}
	out := make([]ScopedTable, 0, len(order))
	for _, n := range order {
		pred, ok := scope(n, "t", 0)
		if !ok {
			return nil, fmt.Errorf("tenantdata: cannot scope %s to its accounts", n)
		}
		out = append(out, ScopedTable{Name: n, Scope: pred})
	}
	return out, nil
}

// ScopedTable is a table with the predicate (alias t) that selects the rows to act on.
type ScopedTable struct {
	Name  string
	Scope string
}

// MemberScope is the predicate selecting the accounts that are members of the
// organization bound to $1 (for the export of users).
func MemberScope(alias string) string {
	return alias + ".id IN (SELECT m.user_id FROM " + ident(MembershipTable) + " m WHERE m.org_id = $1)"
}

// PurgeOrder returns the organization data tables (KindOrg and KindChild,
// without organizations itself) in a safe DELETE order: every table comes
// before the tables it references. memberships is placed as late as the
// foreign keys allow (the spec deletes the data first, then the memberships).
// A cycle between different tables is an error: there is no safe order.
func (s *Schema) PurgeOrder() ([]string, error) {
	nodes := map[string]bool{}
	for _, n := range s.OfKind(KindOrg, KindChild) {
		if n != OrgTable {
			nodes[n] = true
		}
	}
	return topo(nodes, s.FKs, MembershipTable)
}

// PurgeScopes is PurgeOrder with the organization predicate of each table.
func (s *Schema) PurgeScopes() ([]ScopedTable, error) {
	order, err := s.PurgeOrder()
	if err != nil {
		return nil, err
	}
	out := make([]ScopedTable, 0, len(order))
	for _, n := range order {
		pred, ok := s.OrgScope(n, "t")
		if !ok {
			return nil, fmt.Errorf("tenantdata: cannot scope %s to an organization", n)
		}
		out = append(out, ScopedTable{Name: n, Scope: pred})
	}
	return out, nil
}

// topo orders nodes so that a table comes before every table it references
// (children before parents). Ties are broken by name; last is picked only when
// nothing else is ready. Self references are ignored (one DELETE statement
// handles them); any other cycle is an error.
func topo(nodes map[string]bool, fks []FK, last string) ([]string, error) {
	// pending[parent] = number of children that must be deleted before it.
	pending := map[string]int{}
	children := map[string][]string{} // child -> parents it blocks
	seen := map[[2]string]bool{}
	for _, fk := range fks {
		if !nodes[fk.Child] || !nodes[fk.Parent] || fk.Child == fk.Parent || seen[[2]string{fk.Child, fk.Parent}] {
			continue // self references are fine within one DELETE statement
		}
		seen[[2]string{fk.Child, fk.Parent}] = true
		pending[fk.Parent]++
		children[fk.Child] = append(children[fk.Child], fk.Parent)
	}
	var order []string
	done := map[string]bool{}
	for len(order) < len(nodes) {
		var ready []string
		for n := range nodes {
			if !done[n] && pending[n] == 0 {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			var left []string
			for n := range nodes {
				if !done[n] {
					left = append(left, n)
				}
			}
			sort.Strings(left)
			return nil, fmt.Errorf("tenantdata: foreign-key cycle between tables %v: no safe purge order", left)
		}
		sort.Strings(ready)
		pick := ready[0]
		if pick == last && len(ready) > 1 {
			pick = ready[1]
		}
		done[pick] = true
		order = append(order, pick)
		for _, p := range children[pick] {
			pending[p]--
		}
	}
	return order, nil
}

// ExportTable describes how one table is exported.
type ExportTable struct {
	Name     string
	Kind     Kind
	Scope    string   // predicate on alias t, $1 = organization id
	Redacted []string // columns removed from the export (secrets)
	Blobs    []string // bytea columns exported as separate files
	Derived  []string // columns left out because they are derived (full-text search vectors)
}

// ExportPlan lists every table exported for an organization: all organization
// data tables (minus ExcludedFromExport) plus the member accounts (users).
func (s *Schema) ExportPlan() []ExportTable {
	var out []ExportTable
	for _, n := range s.OfKind(KindOrg, KindChild, KindAccount) {
		if _, skip := ExcludedFromExport[n]; skip {
			continue
		}
		t := s.Tables[n]
		var scope string
		switch {
		case n == AccountTable:
			scope = MemberScope("t")
		case t.Kind == KindAccount:
			continue // account-level security rows (e.g. email verifications) are not organization data
		default:
			var ok bool
			if scope, ok = s.OrgScope(n, "t"); !ok {
				continue
			}
		}
		et := ExportTable{Name: n, Kind: t.Kind, Scope: scope}
		for _, c := range t.Columns {
			switch {
			case secretColumn(n, c):
				et.Redacted = append(et.Redacted, c.Name)
			case c.Type == "bytea":
				et.Blobs = append(et.Blobs, c.Name)
			case c.Type == "tsvector":
				et.Derived = append(et.Derived, c.Name)
			}
		}
		out = append(out, et)
	}
	return out
}

// ObjectKeyColumns lists (table, column) pairs of organization tables that hold
// object-store keys (columns named object_key): their objects belong to the
// organization when the bytes live outside Postgres (S3).
func (s *Schema) ObjectKeyColumns() [][2]string {
	var out [][2]string
	for _, n := range s.OfKind(KindOrg, KindChild) {
		if s.Tables[n].has("object_key") {
			out = append(out, [2]string{n, "object_key"})
		}
	}
	return out
}
