package tenantdata

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func cols(names ...string) []Column {
	var out []Column
	for _, n := range names {
		typ := "text"
		if i := strings.IndexByte(n, ':'); i > 0 {
			n, typ = n[:i], n[i+1:]
		}
		out = append(out, Column{Name: n, Type: typ})
	}
	return out
}

func fixture() *Schema {
	return NewSchema(map[string][]Column{
		"organizations":     cols("id", "org_id", "name"),
		"users":             cols("id", "email", "password_hash"),
		"memberships":       cols("org_id", "user_id", "role"),
		"refresh_tokens":    cols("id", "org_id", "user_id", "token_hash"),
		"email_verif":       cols("token_hash", "user_id"),
		"agents":            cols("org_id", "id"),
		"artifacts":         cols("org_id", "id"),
		"artifact_versions": cols("org_id", "artifact_id", "version"),
		"artifact_blobs":    cols("org_id", "id", "artifact_id", "data:bytea"),
		"credentials":       cols("org_id", "id", "ciphertext:bytea", "nonce:bytea", "hint"),
		"folders":           cols("org_id", "id", "parent_id"),
		"notes":             cols("id", "artifact_org", "artifact_id", "body"), // no org_id: child through a FK
		"note_votes":        cols("note_id", "voter"),                          // grandchild
		"audit_logs":        cols("org_id", "id", "hash", "prev_hash"),
		"schema_migrations": cols("version"),
		"org_tombstones":    cols("org_hash", "reason"),
		"lonely":            cols("id", "value"), // nobody's data: must be flagged
	}, []FK{
		{Child: "memberships", Parent: "organizations", ChildCols: []string{"org_id"}, ParentCols: []string{"id"}},
		{Child: "memberships", Parent: "users", ChildCols: []string{"user_id"}, ParentCols: []string{"id"}},
		{Child: "refresh_tokens", Parent: "users", ChildCols: []string{"user_id"}, ParentCols: []string{"id"}},
		{Child: "refresh_tokens", Parent: "organizations", ChildCols: []string{"org_id"}, ParentCols: []string{"id"}},
		{Child: "email_verif", Parent: "users", ChildCols: []string{"user_id"}, ParentCols: []string{"id"}},
		{Child: "agents", Parent: "organizations", ChildCols: []string{"org_id"}, ParentCols: []string{"id"}},
		{Child: "artifact_versions", Parent: "artifacts", ChildCols: []string{"org_id", "artifact_id"}, ParentCols: []string{"org_id", "id"}},
		{Child: "artifact_blobs", Parent: "artifacts", ChildCols: []string{"org_id", "artifact_id"}, ParentCols: []string{"org_id", "id"}},
		{Child: "folders", Parent: "folders", ChildCols: []string{"org_id", "parent_id"}, ParentCols: []string{"org_id", "id"}},
		{Child: "notes", Parent: "artifacts", ChildCols: []string{"artifact_org", "artifact_id"}, ParentCols: []string{"org_id", "id"}},
		{Child: "note_votes", Parent: "notes", ChildCols: []string{"note_id"}, ParentCols: []string{"id"}},
	})
}

func TestClassification(t *testing.T) {
	s := fixture()
	want := map[string]Kind{
		"organizations": KindOrg, "memberships": KindOrg, "refresh_tokens": KindOrg, "agents": KindOrg,
		"notes": KindChild, "note_votes": KindChild,
		"users": KindAccount, "email_verif": KindAccount,
		"schema_migrations": KindGlobal, "org_tombstones": KindGlobal,
		"lonely": KindUnclassified,
	}
	for n, k := range want {
		if got := s.Tables[n].Kind; got != k {
			t.Errorf("%s: kind %s, want %s", n, got, k)
		}
	}
	if u := s.Unclassified(); len(u) != 1 || u[0] != "lonely" {
		t.Fatalf("unclassified = %v", u)
	}
}

func TestPurgeOrderChildrenFirstMembershipsLast(t *testing.T) {
	order, err := fixture().PurgeOrder()
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if _, ok := pos["organizations"]; ok {
		t.Fatal("organizations is deleted separately, last")
	}
	for _, c := range [][2]string{{"artifact_versions", "artifacts"}, {"artifact_blobs", "artifacts"}, {"notes", "artifacts"}, {"note_votes", "notes"}} {
		if pos[c[0]] > pos[c[1]] {
			t.Errorf("%s must be deleted before %s: %v", c[0], c[1], order)
		}
	}
	if order[len(order)-1] != "memberships" {
		t.Errorf("memberships must come last: %v", order)
	}
	for _, n := range []string{"users", "email_verif", "schema_migrations", "lonely"} {
		if _, ok := pos[n]; ok {
			t.Errorf("%s must not be in the organization purge", n)
		}
	}
}

func TestPurgeOrderRejectsCycles(t *testing.T) {
	s := NewSchema(map[string][]Column{"a": cols("org_id", "id", "b_id"), "b": cols("org_id", "id", "a_id")}, []FK{
		{Child: "a", Parent: "b", ChildCols: []string{"b_id"}, ParentCols: []string{"id"}},
		{Child: "b", Parent: "a", ChildCols: []string{"a_id"}, ParentCols: []string{"id"}},
	})
	if _, err := s.PurgeOrder(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle must be an error, got %v", err)
	}
}

func TestScopes(t *testing.T) {
	s := fixture()
	if p, ok := s.OrgScope("agents", "t"); !ok || p != `t."org_id" = $1` {
		t.Fatalf("agents scope = %q %v", p, ok)
	}
	p, ok := s.OrgScope("note_votes", "t")
	if !ok || !strings.Contains(p, `FROM "notes" p0_0 WHERE p0_0."id" = t."note_id"`) || !strings.Contains(p, `FROM "artifacts" p1_0 WHERE p1_0."org_id" = p0_0."artifact_org" AND p1_0."id" = p0_0."artifact_id" AND p1_0."org_id" = $1`) {
		t.Fatalf("grandchild scope = %q", p)
	}
	if _, ok := s.OrgScope("users", "t"); ok {
		t.Fatal("users are not scoped by organization (members are exported through MemberScope)")
	}
	deps, err := s.UserDependents()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range deps {
		names = append(names, d.Name)
		if !strings.Contains(d.Scope, "ANY($1::text[])") {
			t.Errorf("%s user scope = %q", d.Name, d.Scope)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "email_verif,memberships,refresh_tokens" {
		t.Fatalf("user dependents = %v", names)
	}
}

func TestSecretColumns(t *testing.T) {
	secret := []string{
		"users.password_hash", "refresh_tokens.token_hash", "invitations.token_hash", "oauth_states.state_hash",
		"credentials.ciphertext", "credentials.nonce", "org_data_keys.wrapped_dek", "org_llm_credentials.sealed_key",
		"connections.oauth_client_secret_enc", "push_subscriptions.p256dh", "push_subscriptions.auth", "push_subscriptions.endpoint",
		"anything.api_key", "anything.client_secret", "anything.access_token", "anything.refresh_token", "x.new_password", "x.key_hash",
	}
	for _, tc := range secret {
		tbl, col, _ := strings.Cut(tc, ".")
		if !secretColumn(tbl, Column{Name: col, Type: "text"}) {
			t.Errorf("%s must be redacted", tc)
		}
	}
	for _, tc := range []string{"audit_logs.hash", "audit_logs.prev_hash", "artifact_versions.content_hash", "drive_files.sha256",
		"approval_executions.args_hash", "credentials.hint", "users.email", "org_llm_credentials.key_last4", "messages.text", "refresh_tokens.family_id"} {
		tbl, col, _ := strings.Cut(tc, ".")
		if secretColumn(tbl, Column{Name: col, Type: "text"}) {
			t.Errorf("%s must be exported", tc)
		}
	}
	// bytea: default deny, except the file contents.
	if !secretColumn("new_table", Column{Name: "payload", Type: "bytea"}) {
		t.Error("an unknown bytea column must be redacted by default")
	}
	if secretColumn("drive_objects", Column{Name: "data", Type: "bytea"}) || secretColumn("artifact_blobs", Column{Name: "data", Type: "bytea"}) {
		t.Error("file contents are exported")
	}
	plan := fixture().ExportPlan()
	byName := map[string]ExportTable{}
	for _, e := range plan {
		byName[e.Name] = e
	}
	if e := byName["credentials"]; strings.Join(e.Redacted, ",") != "ciphertext,nonce" || len(e.Blobs) != 0 {
		t.Errorf("credentials export = %+v", e)
	}
	if e := byName["artifact_blobs"]; strings.Join(e.Blobs, ",") != "data" {
		t.Errorf("artifact_blobs export = %+v", e)
	}
	if e, ok := byName["users"]; !ok || strings.Join(e.Redacted, ",") != "password_hash" || !strings.Contains(e.Scope, "memberships") {
		t.Errorf("users export = %+v", e)
	}
	for _, n := range []string{"email_verif", "schema_migrations", "org_tombstones", "lonely"} {
		if _, ok := byName[n]; ok {
			t.Errorf("%s must not be exported", n)
		}
	}
}

func TestSafePath(t *testing.T) {
	for in, want := range map[string]string{
		"orgs/a/drive/x.txt": "orgs/a/drive/x.txt",
		"../../etc/passwd":   "etc/passwd",
		"/abs//x":            "abs/x",
		"a b?c":              "a_b_c",
		"":                   "_",
	} {
		if got := safePath(in); got != want {
			t.Errorf("safePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMigrationsGuard is the database-free half of the guard against forgetting
// new tables (the Postgres half is TestPGGuardEveryOrgTableIsCovered): every
// CREATE TABLE of the migrations must be organization data (an org_id column),
// reach organization data through a REFERENCES clause, hang from users, or be
// declared in GlobalTables. A new table that is none of these fails here.
func TestMigrationsGuard(t *testing.T) {
	files, err := filepath.Glob("../infrastructure/postgres/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations not found: %v", err)
	}
	sort.Strings(files)
	createRe := regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS\s+(\w+)\s*\((.*?)\n\);`)
	alterOrg := regexp.MustCompile(`(?i)ALTER TABLE\s+(\w+)\s+ADD COLUMN IF NOT EXISTS\s+org_id\b`)
	refRe := regexp.MustCompile(`(?i)REFERENCES\s+(\w+)`)
	bodies := map[string]string{}
	var order []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sql := strings.ReplaceAll(string(b), "\r\n", "\n")
		for _, m := range createRe.FindAllStringSubmatch(sql, -1) {
			name := strings.ToLower(m[1])
			if _, ok := bodies[name]; !ok {
				order = append(order, name)
			}
			bodies[name] += m[2]
		}
		for _, m := range alterOrg.FindAllStringSubmatch(sql, -1) {
			bodies[strings.ToLower(m[1])] += "\n org_id TEXT"
		}
	}
	orgCol := regexp.MustCompile(`(?im)^\s*org_id\s+TEXT`)
	kind := map[string]string{}
	for changed := true; changed; {
		changed = false
		for _, n := range order {
			if kind[n] != "" {
				continue
			}
			body := bodies[n]
			switch {
			case GlobalTables[n] != "":
				kind[n] = "global"
			case orgCol.MatchString(body):
				kind[n] = "org"
			case n == AccountTable:
				kind[n] = "account"
			default:
				for _, r := range refRe.FindAllStringSubmatch(body, -1) {
					switch kind[strings.ToLower(r[1])] {
					case "org", "child":
						kind[n] = "child"
					case "account":
						if kind[n] == "" {
							kind[n] = "account"
						}
					}
				}
			}
			if kind[n] != "" {
				changed = true
			}
		}
	}
	if len(order) < 40 {
		t.Fatalf("parsed only %d tables from the migrations: the parser is broken", len(order))
	}
	for _, n := range order {
		if kind[n] == "" {
			t.Errorf("table %q is neither organization data (org_id or a foreign key to it), account data, nor declared in tenantdata.GlobalTables: "+
				"classify it so that the GDPR export and purge cover it (internal/tenantdata/README.md)", n)
		}
	}
}
