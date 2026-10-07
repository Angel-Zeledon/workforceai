package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTx records statements; unimplemented pgx.Tx methods panic (nil embed).
type fakeTx struct {
	pgx.Tx
	log       *[]string
	committed *bool
	rolled    *bool
	execErr   error
}

func (f fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	*f.log = append(*f.log, sql+" | "+argsString(args))
	return pgconn.CommandTag{}, f.execErr
}
func (f fakeTx) Commit(context.Context) error   { *f.committed = true; return nil }
func (f fakeTx) Rollback(context.Context) error { *f.rolled = true; return nil }

func argsString(a []any) string {
	var s []string
	for _, v := range a {
		s = append(s, v.(string))
	}
	return strings.Join(s, ",")
}

type fakeDB struct {
	log               []string
	committed, rolled bool
	execErr           error
	beginCalls        int
}

func (d *fakeDB) Begin(context.Context) (pgx.Tx, error) {
	d.beginCalls++
	return fakeTx{log: &d.log, committed: &d.committed, rolled: &d.rolled, execErr: d.execErr}, nil
}

func TestWithOrgTxSetsLocalOrgFirstAndCommits(t *testing.T) {
	db := &fakeDB{}
	err := WithOrgTx(bg, db, "org-1", func(tx pgx.Tx) error {
		tx.Exec(bg, "SELECT 1 FROM tasks")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.log) != 2 || !strings.HasPrefix(db.log[0], "SELECT set_config('app.org_id', $1, true) | org-1") ||
		db.log[1] != "SELECT 1 FROM tasks | " {
		t.Fatalf("statements = %q", db.log)
	}
	if !db.committed || db.rolled {
		t.Fatalf("committed=%v rolled=%v", db.committed, db.rolled)
	}
}

func TestWithOrgTxRollsBackOnError(t *testing.T) {
	db := &fakeDB{}
	boom := errors.New("boom")
	if err := WithOrgTx(bg, db, "org-1", func(pgx.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if db.committed || !db.rolled {
		t.Fatalf("committed=%v rolled=%v", db.committed, db.rolled)
	}
}

func TestWithOrgTxRollsBackOnPanicAndSetFailure(t *testing.T) {
	db := &fakeDB{}
	func() {
		defer func() { recover() }()
		WithOrgTx(bg, db, "org-1", func(pgx.Tx) error { panic("x") })
	}()
	if db.committed || !db.rolled {
		t.Fatalf("panic: committed=%v rolled=%v", db.committed, db.rolled)
	}
	db = &fakeDB{execErr: errors.New("set failed")}
	called := false
	if err := WithOrgTx(bg, db, "org-1", func(pgx.Tx) error { called = true; return nil }); err == nil || called {
		t.Fatalf("fn must not run when SET fails (err=%v called=%v)", err, called)
	}
	if db.committed || !db.rolled {
		t.Fatal("must roll back")
	}
}

func TestWithOrgTxRejectsBadOrgIDs(t *testing.T) {
	for _, id := range []string{"", "a'; DROP TABLE tasks;--", "x y", strings.Repeat("a", 65), "o\n1", "a,b"} {
		db := &fakeDB{}
		if err := WithOrgTx(bg, db, id, func(pgx.Tx) error { t.Fatal("fn ran"); return nil }); err == nil {
			t.Errorf("org id %q accepted", id)
		}
		if db.beginCalls != 0 {
			t.Errorf("org id %q: began a transaction", id)
		}
	}
	// A plain UUID (Phase 1 demo org) is fine.
	if err := WithOrgTx(bg, &fakeDB{}, "00000000-0000-0000-0000-000000000001", func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestWithRequestTxUsesPrincipalOrg(t *testing.T) {
	db := &fakeDB{}
	if err := WithRequestTx(bg, db, func(pgx.Tx) error { return nil }); err == nil {
		t.Fatal("must fail closed without principal")
	}
	ctx := WithPrincipal(bg, Principal{UserID: "u", OrgID: "org-42", Role: RoleMember})
	if err := WithRequestTx(ctx, db, func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(db.log[0], "| org-42") {
		t.Fatalf("log = %q", db.log)
	}
}
