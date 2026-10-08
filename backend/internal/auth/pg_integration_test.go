package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Postgres integration tests. They run only when TEST_DATABASE_URL points to a
// database where the user can CREATE SCHEMA / CREATE ROLE (e.g. a throwaway
// local container):
//
//	docker run --rm -p 5433:5432 -e POSTGRES_PASSWORD=pw postgres:16
//	TEST_DATABASE_URL=postgres://postgres:pw@localhost:5433/postgres go test ./internal/auth/ -run PG
//
// Everything happens inside a temporary schema that is dropped afterwards.
func pgConn(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "authtest_" + randomHex(4)
	mustExec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("%v\nSQL: %.200s", err, sql)
		}
	}
	mustExec("CREATE SCHEMA " + schema)
	mustExec("SET search_path TO " + schema)
	t.Cleanup(func() {
		conn.Exec(ctx, "RESET ROLE")
		conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		conn.Close(ctx)
	})
	for _, f := range []string{
		"../infrastructure/postgres/migrations/001_init.sql",
		"../infrastructure/postgres/migrations/201_auth_tables.sql",
		"../infrastructure/postgres/migrations/202_rls_policies.sql",
		"../infrastructure/postgres/migrations/300_invitations.sql",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(string(b))
	}
	// Idempotency: the phase-2 migrations must be re-runnable.
	for _, f := range []string{"../infrastructure/postgres/migrations/201_auth_tables.sql", "../infrastructure/postgres/migrations/202_rls_policies.sql"} {
		b, _ := os.ReadFile(f)
		mustExec(string(b))
	}
	mustExec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authtest_app') THEN CREATE ROLE authtest_app NOLOGIN; END IF;
	END $$`)
	mustExec("GRANT USAGE ON SCHEMA " + schema + " TO authtest_app")
	mustExec("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schema + " TO authtest_app")
	mustExec("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA " + schema + " TO authtest_app")
	return conn
}

func TestPGStoreContract(t *testing.T) {
	conn := pgConn(t)
	storeContract(t, NewPGStore(conn))
}

func TestPGInvitationContract(t *testing.T) {
	invitationContract(t, NewPGStore(pgConn(t)))
}

// TestPGInvitationsRLS: as the application role, an organization only sees its
// own invitations, and a transaction without a tenant only sees the row whose
// token hash it set.
func TestPGInvitationsRLS(t *testing.T) {
	conn := pgConn(t)
	ctx := context.Background()
	orgA, orgB := "org-a-"+randomHex(3), "org-b-"+randomHex(3)
	for _, org := range []string{orgA, orgB} {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`INSERT INTO organizations (id, name, slug) VALUES ('%[1]s','n','slug-%[1]s')`, org)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO invitations (id, org_id, email, role, token_hash, invited_by, expires_at)
			VALUES ($1,$2,'x@example.com','member',$3,'u', now() + interval '1 hour')`, "i-"+org, org, "hash-"+org); err != nil {
			t.Fatal(err)
		}
	}
	count := func(setup string, args ...any) int {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if setup != "" {
			if _, err := tx.Exec(ctx, setup, args...); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE authtest_app"); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM invitations").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT set_config('app.org_id', $1, true)`, orgA); n != 1 {
		t.Fatalf("org A sees %d invitations, want 1", n)
	}
	if n := count(""); n != 0 {
		t.Fatalf("no tenant sees %d invitations, want 0", n)
	}
	if n := count(`SELECT set_config('app.invitation_hash', $1, true)`, "hash-"+orgB); n != 1 {
		t.Fatalf("hash scope sees %d invitations, want 1", n)
	}
}

func TestPGRowLevelSecurityIsolatesTenants(t *testing.T) {
	conn := pgConn(t)
	ctx := context.Background()
	orgA, orgB := "org-a-"+randomHex(3), "org-b-"+randomHex(3)

	asApp := func(org string, fn func(tx pgx.Tx) error) error {
		return WithOrgTx(ctx, conn, org, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE authtest_app"); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	count := func(tx pgx.Tx, table string) int {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}

	// Each tenant creates its org and one row in every tenant table.
	for _, org := range []string{orgA, orgB} {
		err := asApp(org, func(tx pgx.Tx) error {
			stmts := []string{
				fmt.Sprintf(`INSERT INTO organizations (id, name, slug) VALUES ('%[1]s','n','slug-%[1]s')`, org),
				fmt.Sprintf(`INSERT INTO agents (org_id, id, name, role, title) VALUES ('%s','ag','n','r','t')`, org),
				fmt.Sprintf(`INSERT INTO requests (id, org_id, text, status) VALUES ('rq-%[1]s','%[1]s','x','running')`, org),
				fmt.Sprintf(`INSERT INTO tasks (id, org_id, request_id, title, agent_id, status) VALUES ('t-%[1]s','%[1]s','rq','t','ag','pending')`, org),
				fmt.Sprintf(`INSERT INTO conversations (id, org_id, title) VALUES ('c-%[1]s','%[1]s','t')`, org),
				fmt.Sprintf(`INSERT INTO messages (id, org_id, conversation_id, from_id, to_id, kind, text) VALUES ('m-%[1]s','%[1]s','c','a','b','chat','x')`, org),
				fmt.Sprintf(`INSERT INTO approvals (id, org_id, task_id, agent_id, action, title, risk, status) VALUES ('ap-%[1]s','%[1]s','t','a','x','t','low','pending')`, org),
				fmt.Sprintf(`INSERT INTO reports (id, org_id, request_id, title) VALUES ('r-%[1]s','%[1]s','rq','t')`, org),
				fmt.Sprintf(`INSERT INTO memories (org_id, agent_id, scope, key, value) VALUES ('%s','a','s','k','v')`, org),
				fmt.Sprintf(`INSERT INTO events (id, org_id, type) VALUES ('e-%[1]s','%[1]s','t')`, org),
				fmt.Sprintf(`INSERT INTO audit_logs (id, org_id, actor, action) VALUES ('al-%[1]s','%[1]s','u','a')`, org),
				fmt.Sprintf(`INSERT INTO activity (id, org_id, kind, text) VALUES ('ac-%[1]s','%[1]s','k','t')`, org),
			}
			for _, s := range stmts {
				if _, err := tx.Exec(ctx, s); err != nil {
					return fmt.Errorf("%s: %w", s, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed %s: %v", org, err)
		}
	}

	tables := []string{"organizations", "agents", "requests", "tasks", "conversations", "messages",
		"approvals", "reports", "memories", "events", "audit_logs", "activity"}

	// 1. Each tenant sees exactly its own single row per table.
	for _, org := range []string{orgA, orgB} {
		asApp(org, func(tx pgx.Tx) error {
			for _, tb := range tables {
				if n := count(tx, tb); n != 1 {
					t.Errorf("org %s sees %d rows in %s, want 1", org, n, tb)
				}
			}
			return nil
		})
	}

	// 2. No tenant set => nothing visible (fail closed), even with the app role.
	tx, _ := conn.Begin(ctx)
	tx.Exec(ctx, "SET LOCAL ROLE authtest_app")
	for _, tb := range tables {
		if n := count(tx, tb); n != 0 {
			t.Errorf("no tenant: %d rows visible in %s", n, tb)
		}
	}
	tx.Rollback(ctx)

	// 3. The tenant setting does not leak to the next transaction on the same connection.
	asApp(orgA, func(pgx.Tx) error { return nil })
	tx, _ = conn.Begin(ctx)
	var setting string
	tx.QueryRow(ctx, "SELECT coalesce(current_setting('app.org_id', true), '')").Scan(&setting)
	tx.Rollback(ctx)
	if setting != "" {
		t.Errorf("app.org_id leaked across transactions: %q", setting)
	}

	// 4. Cross-tenant writes are rejected or affect nothing.
	err := asApp(orgA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO tasks (id, org_id, request_id, title, agent_id, status) VALUES ('evil','%s','rq','t','ag','pending')`, orgB))
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Errorf("insert into other tenant must violate RLS, got %v", err)
	}
	asApp(orgA, func(tx pgx.Tx) error {
		for _, q := range []string{
			fmt.Sprintf(`UPDATE tasks SET title='pwned' WHERE org_id='%s'`, orgB),
			fmt.Sprintf(`DELETE FROM agents WHERE org_id='%s'`, orgB),
			`UPDATE tasks SET title='pwned'`, // unscoped: only own rows can match
		} {
			tag, err := tx.Exec(ctx, q)
			if err != nil {
				t.Errorf("%s: %v", q, err)
			}
			if strings.HasPrefix(q, "UPDATE tasks SET title='pwned' WHERE") || strings.HasPrefix(q, "DELETE") {
				if tag.RowsAffected() != 0 {
					t.Errorf("%s affected %d rows", q, tag.RowsAffected())
				}
			}
		}
		// Moving a row to another tenant via UPDATE is blocked by WITH CHECK.
		_, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE tasks SET org_id='%s'`, orgB))
		if err == nil {
			t.Error("re-parenting a row to another tenant must fail")
		}
		return err // aborts tx
	})
	asApp(orgB, func(tx pgx.Tx) error {
		var title string
		tx.QueryRow(ctx, "SELECT title FROM tasks").Scan(&title)
		if title == "pwned" {
			t.Error("org B data was modified by org A")
		}
		return nil
	})
}
