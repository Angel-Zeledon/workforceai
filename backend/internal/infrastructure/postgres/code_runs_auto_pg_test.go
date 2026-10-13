package postgres

import (
	"context"
	"testing"
	"time"
)

// CountAutoRunsOfTask: only started desk-mode runs of that task, per organization (TEST_DATABASE_URL).
func TestCodeRunStoreCountsAutoRunsPerTask(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	ctx := context.Background()
	cs := &CodeRunStore{S: env.Store}
	for i, id := range []string{"r1", "r2", "r3"} {
		r := newRun("org-a", id, "ap"+id)
		r.AutoRun = i < 2 // r3 is a normal approved run
		if err := cs.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := cs.CountAutoRunsOfTask(ctx, "org-a", "t1"); err != nil || n != 0 {
		t.Fatalf("not started yet: %d %v", n, err)
	}
	for _, id := range []string{"r1", "r3"} {
		if ok, err := cs.BeginRun(ctx, "org-a", id, "ap"+id, []string{"u"}, time.Now().UTC()); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if n, _ := cs.CountAutoRunsOfTask(ctx, "org-a", "t1"); n != 1 {
		t.Fatalf("only the started auto run counts: %d", n)
	}
	if n, _ := cs.CountAutoRunsOfTask(ctx, "org-a", "other-task"); n != 0 {
		t.Fatalf("other task: %d", n)
	}
	if n, _ := cs.CountAutoRunsOfTask(ctx, "org-b", "t1"); n != 0 {
		t.Fatalf("other organization: %d", n)
	}
}
