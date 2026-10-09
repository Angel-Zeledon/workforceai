package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// Integration tests against a real Postgres. They run only when
// TEST_DATABASE_URL points to a database where the user may CREATE SCHEMA and
// CREATE ROLE (superuser, e.g. a throwaway container):
//
//	docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=pw postgres:16-alpine
//	TEST_DATABASE_URL=postgres://postgres:pw@localhost:55432/postgres?sslmode=disable go test ./internal/infrastructure/postgres/
//
// Each test runs the REAL embedded migrations (001..210) in a temporary schema
// and connects through Open, i.e. exactly like the server does (SET ROLE app_user).

type testEnv struct {
	*Store
	schema string
	rawURL string
}

func testStore(t *testing.T) *testEnv {
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
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	schema := "rlstest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	st, err := Open(ctx, Options{URL: u.String(), AppRole: "app_user"})
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(ctx)
	})
	return &testEnv{Store: st, schema: schema, rawURL: u.String()}
}

func seedOrg(t *testing.T, st *Store, id string) {
	t.Helper()
	if err := st.Seed(context.Background(), domain.Organization{ID: id, Name: id, Slug: id, BudgetUSD: 10}, roles.SeedAgents()); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func populate(t *testing.T, st *Store, org string) (taskID, reqID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	reqID, taskID = "rq-"+org, "tk-"+org
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateRequest(ctx, org, domain.Request{ID: reqID, Text: "secret of " + org, Status: domain.RequestRunning, CreatedAt: now}))
	must(st.CreateTask(ctx, org, domain.Task{ID: taskID, RequestID: reqID, Title: "task " + org, AgentID: "sales", Status: domain.TaskPending, CreatedAt: now, Depth: 1}))
	must(st.CreateConversation(ctx, org, domain.Conversation{ID: "cv-" + org, Title: "c", Participants: []string{"user"}, LastMessageAt: now}))
	must(st.AddMessage(ctx, org, domain.Message{ID: "ms-" + org, ConversationID: "cv-" + org, From: "user", To: "assistant", Kind: "chat", Text: "hi", TS: now}))
	must(st.CreateApproval(ctx, org, domain.Approval{ID: "ap-" + org, TaskID: taskID, AgentID: "sales", Action: "send_proposal", Title: "t", Risk: "high", Status: domain.ApprovalPending, CreatedAt: now}))
	must(st.CreateReport(ctx, org, domain.Report{ID: "rp-" + org, RequestID: reqID, Title: "r", CreatedAt: now}))
	must(st.SetMemory(ctx, org, "sales", domain.Memory{Scope: "agent", Key: "k", Value: "v-" + org}))
	must(st.SaveEvent(ctx, org, domain.Event{ID: "ev-" + org, Type: "x", TS: now}))
	must(st.AddActivity(ctx, org, domain.ActivityItem{ID: "ac-" + org, TS: now, Kind: "k", Text: "t"}))
	must(st.AddAudit(ctx, org, domain.AuditLog{ID: "au-" + org, Actor: "u", Action: "a"}))
	must(st.AddCost(ctx, org, reqID, taskID, 1.5))
	must(st.AddUsage(ctx, org, domain.UsageEntry{ID: "us-" + org, RequestID: reqID, TaskID: taskID, AgentID: "sales", Kind: domain.UsageRunTask, CostUSD: 1.5, Tools: []string{"email.send"}}))
	must(st.SetBudgetCap(ctx, org, domain.ScopeAgent, "sales", 5))
	return taskID, reqID
}

func TestMigrationsApplyOnceAndInOrder(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	// schema_migrations is not readable by app_user: check through an owner connection.
	owner, err := pgx.Connect(ctx, env.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	rows, err := owner.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	// Every embedded migration, in name order (the list is not hard-coded so
	// that new migrations do not break this test).
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		want = append(want, e.Name())
	}
	sort.Strings(want)
	if strings.Join(versions, ",") != strings.Join(want, ",") {
		t.Fatalf("versions = %v, want %v", versions, want)
	}
	// Re-running is a no-op (restart of the server).
	if err := Migrate(ctx, env.rawURL); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestPoolRunsAsPlainRoleSoRLSApplies(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")

	var user string
	var super, bypass bool
	if err := st.pool.QueryRow(ctx, `SELECT current_user, r.rolsuper, r.rolbypassrls FROM pg_roles r WHERE r.rolname = current_user`).Scan(&user, &super, &bypass); err != nil {
		t.Fatal(err)
	}
	if user != "app_user" || super || bypass {
		t.Fatalf("pool runs as %q super=%v bypassrls=%v; must be a plain app_user", user, super, bypass)
	}
	// Fail closed: a query that sets no tenant sees nothing, although rows exist.
	for _, tb := range []string{"organizations", "agents", "tasks", "requests", "audit_logs"} {
		var n int
		if err := st.pool.QueryRow(ctx, "SELECT count(*) FROM "+tb).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("without app.org_id %d rows of %s are visible", n, tb)
		}
	}
	// And the setting never leaks from WithOrgTx into the next use of a connection.
	if err := st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var setting string
	if err := st.pool.QueryRow(ctx, `SELECT coalesce(current_setting('app.org_id', true), '')`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != "" {
		t.Fatalf("app.org_id leaked: %q", setting)
	}
}

func TestStoreIsolatesTwoOrganizations(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	taskA, reqA := populate(t, st, "org-a")
	taskB, reqB := populate(t, st, "org-b")

	// Reads through the Store API only return the caller's rows.
	for _, c := range []struct{ org, other, task, req, otherTask, otherReq string }{
		{"org-a", "org-b", taskA, reqA, taskB, reqB},
		{"org-b", "org-a", taskB, reqB, taskA, reqA},
	} {
		ts, err := st.ListTasks(ctx, c.org, "", "")
		if err != nil || len(ts) != 1 || ts[0].ID != c.task {
			t.Fatalf("%s tasks = %v, %v", c.org, ts, err)
		}
		rs, _ := st.ListRequests(ctx, c.org)
		if len(rs) != 1 || rs[0].ID != c.req {
			t.Fatalf("%s requests = %v", c.org, rs)
		}
		ags, _ := st.ListAgents(ctx, c.org)
		if len(ags) != len(roles.SeedAgents()) {
			t.Fatalf("%s agents = %d", c.org, len(ags))
		}
		if _, err := st.GetTask(ctx, c.org, c.otherTask); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s reads %s: err = %v, want ErrNotFound", c.org, c.otherTask, err)
		}
		if _, err := st.GetRequest(ctx, c.org, c.otherReq); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s reads request of other tenant: %v", c.org, err)
		}
		if _, err := st.GetApproval(ctx, c.org, "ap-"+c.other); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s reads approval of other tenant: %v", c.org, err)
		}
		// W5: the filtered approvals query returns this tenant's approval of the
		// request (and the extra task id), never another tenant's.
		if aps, err := st.ListApprovalsByRequest(ctx, c.org, c.req, []string{"project:x"}); err != nil || len(aps) != 1 || aps[0].ID != "ap-"+c.org {
			t.Errorf("%s approvals by request = %v, %v", c.org, aps, err)
		}
		if aps, _ := st.ListApprovalsByRequest(ctx, c.org, c.otherReq, nil); len(aps) != 0 {
			t.Errorf("%s sees approvals of another tenant's request: %v", c.org, aps)
		}
		mem, _ := st.ListMemory(ctx, c.org, "sales")
		if len(mem) != 1 || mem[0].Value != "v-"+c.org {
			t.Errorf("%s memory = %v", c.org, mem)
		}
		if cost, _ := st.OrgCost(ctx, c.org); cost != 1.5 {
			t.Errorf("%s cost = %v (must not include the other tenant)", c.org, cost)
		}
	}

	// Writes by org A can't touch org B's rows, even knowing their ids.
	if err := st.UpdateTask(ctx, "org-a", domain.Task{ID: taskB, Status: domain.TaskDone}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update of other tenant's task: %v", err)
	}
	if err := st.UpdateApproval(ctx, "org-a", domain.Approval{ID: "ap-org-b", Status: domain.ApprovalApproved}); err == nil {
		t.Error("approving another tenant's approval must fail")
	}
	if b, _ := st.GetTask(ctx, "org-b", taskB); b.Status != domain.TaskPending {
		t.Errorf("org-b task changed by org-a: %s", b.Status)
	}

	// Raw SQL in org A's transaction (no WHERE org_id at all): RLS alone restricts it.
	err := st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		for _, tb := range []string{"organizations", "agents", "tasks", "requests", "conversations", "messages", "approvals", "reports", "memories", "events", "audit_logs", "activity", "usage_entries", "budget_caps"} {
			var rows, orgs int
			if err := tx.QueryRow(ctx, "SELECT count(*), count(DISTINCT org_id) FROM "+tb).Scan(&rows, &orgs); err != nil {
				return err
			}
			if rows == 0 || orgs != 1 {
				t.Errorf("%s: org-a transaction sees %d rows of %d tenants", tb, rows, orgs)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Cross-tenant insert and re-parenting are rejected by WITH CHECK.
	err = st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO activity (id, org_id, kind, text) VALUES ('evil','org-b','k','t')`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Errorf("cross-tenant insert: %v", err)
	}
	err = st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tasks SET org_id='org-b'`)
		return err
	})
	if err == nil {
		t.Error("re-parenting a task to another tenant must fail")
	}
}

func TestResetOnlyAffectsOwnTenant(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	populate(t, st, "org-a")
	populate(t, st, "org-b")
	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := st.ListTasks(ctx, "org-a", "", ""); len(ts) != 0 {
		t.Fatalf("org-a tasks after reset: %d", len(ts))
	}
	if ts, _ := st.ListTasks(ctx, "org-b", "", ""); len(ts) != 1 {
		t.Fatalf("org-b tasks after org-a reset: %d", len(ts))
	}
}

func TestAuditLogsAreAppendOnlyForTheApp(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.AddAudit(ctx, "org-a", domain.AuditLog{ID: "x1", Actor: "u", Action: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE audit_logs SET actor='evil'`, `DELETE FROM audit_logs`} {
		err := st.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error { _, e := tx.Exec(ctx, q); return e })
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s must be denied, got %v", q, err)
		}
	}
}

// The fixed `demo` org keeps working with RLS on (AUTH_ENABLED=false).
func TestDemoOrgWorksWithRLS(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	if err := st.Seed(ctx, domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	ags, err := st.ListAgents(ctx, domain.DemoOrgID)
	if err != nil || len(ags) != len(roles.SeedAgents()) {
		t.Fatalf("agents = %d, %v", len(ags), err)
	}
	if err := st.Seed(ctx, domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatalf("seed must be idempotent: %v", err)
	}
}

// Registration (auth module) works on the RLS-restricted pool and every new
// organization is isolated from the others.
func TestAuthRegistrationOnRLSPool(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	svc, err := auth.NewService(auth.Config{
		Store:          auth.NewPGStore(st.Pool()),
		Secret:         []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := func(email string) *auth.Session {
		s, err := svc.Register(ctx, auth.RegisterInput{Email: email, Password: "Correct-Horse-9", Name: "N", OrgName: "Org " + email}, auth.ClientMeta{})
		if err != nil {
			t.Fatalf("register %s: %v", email, err)
		}
		return s
	}
	a, b := reg("a@example.com"), reg("b@example.com")
	if a.OrgID == b.OrgID {
		t.Fatal("each registration must create its own org")
	}
	p, err := svc.Authenticate(ctx, a.AccessToken)
	if err != nil || p.OrgID != a.OrgID || p.Role != auth.RoleOwner {
		t.Fatalf("principal = %+v, %v", p, err)
	}
	if err := st.Seed(ctx, domain.Organization{ID: a.OrgID, Name: "A", Slug: "a-slug", BudgetUSD: 1}, roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	if ags, _ := st.ListAgents(ctx, b.OrgID); len(ags) != 0 {
		t.Fatalf("tenant B sees %d agents of A", len(ags))
	}
	if ags, _ := st.ListAgents(ctx, a.OrgID); len(ags) == 0 {
		t.Fatal("tenant A lost its agents")
	}
}
