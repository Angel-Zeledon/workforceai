package officeaudience_test

import (
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// help.notice carries the need of a question and no task: it reaches who may see the request of the question (here a
// request asked from a private office and taken by a General agent) and the person the question is for, not the
// rest of the organization. A burst that mixes requests of several offices is sealed to the most restrictive one.
func TestHelpNoticeIsSealedToTheRequestOfTheQuestion(t *testing.T) {
	f := newFix(t)
	f.install(nil)
	ctx, org := f.ctx, f.org
	now := time.Now().UTC()
	if err := f.store.CreateRequest(ctx, org, domain.Request{ID: "r-off", Text: "SECRETO", Status: domain.RequestRunning, CreatedAt: now, OfficeID: "d-priv"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateTask(ctx, org, domain.Task{ID: "t-off", RequestID: "r-off", AgentID: "operations", OfficeID: "d-priv", Title: "SECRETO"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutRunMeta(ctx, org, application.RunMeta{RequestID: "r-off", RequestedBy: "u-pri"}); err != nil {
		t.Fatal(err)
	}
	qs := &application.Questions{Store: f.store, Rec: f.rec, NoticeWindow: 60 * time.Millisecond}
	ask := func(task, req, group, askedFor string) {
		t.Helper()
		if _, _, err := qs.Ask(ctx, application.Question{TaskID: task, RequestID: req, AgentID: "operations", GroupKey: group, AskedFor: askedFor, Need: "SECRETO-necesidad"}); err != nil {
			t.Fatal(err)
		}
	}
	got := func(user string, g map[string][]string) bool { return slices.Contains(g[user], domain.EvHelpNotice) }

	// a question of the private office's request, for an admin outside it
	ask("t-off", "r-off", "request:r-off", "u-boss")
	g := f.got()
	for u, want := range map[string]bool{"u-pri": true, "u-boss": true, "u-ana": false, "u-eve": false} {
		if got(u, g) != want {
			t.Fatalf("help.notice of a private office's question: %s got=%v want=%v (%v)", u, !want, want, g)
		}
	}

	// a burst of one group that mixes a General request with the private office's: the summary is sealed to the private one
	ask("t-gen", "r-gen", "batch:mix", "u-boss")
	f.got() // the first one of the burst goes out at once, about General
	ask("t-off", "r-off", "batch:mix", "u-boss")
	time.Sleep(250 * time.Millisecond)
	g = f.got()
	for u, want := range map[string]bool{"u-pri": true, "u-boss": true, "u-ana": false, "u-eve": false} {
		if got(u, g) != want {
			t.Fatalf("summary notice of a mixed burst: %s got=%v want=%v (%v)", u, !want, want, g)
		}
	}

	// a question of a General request still reaches everybody it did
	ask("t-gen", "r-gen", "request:r-gen", "u-ana")
	if g = f.got(); !got("u-ana", g) || !got("u-boss", g) {
		t.Fatalf("a General question's notice must keep reaching the organization: %v", g)
	}
}
