package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// V2 (migrations 470-472): profile versions, traces under RLS, purge, steer notes, down migrations.
func TestV2ProfilesTracesAndSteerUnderRLS(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	p := application.AgentProfile{AgentID: "sales", ProfileSnapshot: application.ProfileSnapshot{Instructions: "x", Creativity: "high", Skills: []string{"a"}}, Version: 1, UpdatedBy: "u"}
	v := application.ProfileVersion{AgentID: "sales", Version: 1, Author: "u", CreatedAt: time.Now().UTC(), Snapshot: p.ProfileSnapshot}
	if err := st.SaveAgentProfile(ctx, "org-a", p, v); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgentProfile(ctx, "org-a", p, v); !errors.Is(err, domain.ErrConflict) { // stale second editor
		t.Fatalf("stale save = %v", err)
	}
	if _, ok, _ := st.GetAgentProfile(ctx, "org-b", "sales"); ok {
		t.Fatal("org-b reads org-a profile")
	}
	if vs, _ := st.ListProfileVersions(ctx, "org-b", "sales"); len(vs) != 0 {
		t.Fatal("org-b reads org-a versions")
	}

	old := application.Trace{ID: "t1", AgentID: "sales", TaskID: "k1", CreatedAt: time.Now().UTC().Add(-48 * time.Hour), Status: "ok"}
	cur := application.Trace{ID: "t2", AgentID: "sales", TaskID: "k1", CreatedAt: time.Now().UTC(), Status: "ok"}
	for _, tr := range []application.Trace{old, cur} {
		if err := st.AddTrace(ctx, "org-a", tr); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := st.AppendTraceEvent(ctx, "org-a", "k1", application.TraceEvent{Type: "retry", TS: time.Now()}); err != nil || !ok {
		t.Fatalf("append = %v %v", ok, err)
	}
	got, err := st.GetTrace(ctx, "org-a", "sales", "t2")
	if err != nil || got.Attempt != 2 || len(got.Events) != 1 {
		t.Fatalf("trace = %+v, %v", got, err)
	}
	if _, err := st.GetTrace(ctx, "org-b", "sales", "t2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads org-a trace: %v", err)
	}
	if trs, _ := st.ListTraces(ctx, "org-b", "sales", 10, time.Time{}); len(trs) != 0 {
		t.Fatal("org-b lists org-a traces")
	}
	if n, err := st.PurgeTraces(ctx, "org-b", time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("org-b purge touched org-a: %d %v", n, err)
	}
	if n, err := st.PurgeTraces(ctx, "org-a", time.Now().Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if on, _ := st.GetTraceStorePrompts(ctx, "org-a"); !on {
		t.Fatal("store_prompts must default to true")
	}
	_ = st.SetTraceStorePrompts(ctx, "org-a", false)
	if on, _ := st.GetTraceStorePrompts(ctx, "org-a"); on {
		t.Fatal("store_prompts=false not persisted")
	}

	n := application.SteerNote{ID: "n1", TaskID: "k1", AgentID: "sales", Note: "hi", Author: "u", CreatedAt: time.Now().UTC()}
	if err := st.AddSteerNote(ctx, "org-a", n); err != nil {
		t.Fatal(err)
	}
	if ns, _ := st.ListSteerNotes(ctx, "org-b", "k1"); len(ns) != 0 {
		t.Fatal("org-b reads org-a notes")
	}
	_ = st.MarkSteerDelivered(ctx, "org-a", []string{"n1"}, time.Now())
	if ns, _ := st.ListSteerNotes(ctx, "org-a", "k1"); len(ns) != 1 || ns[0].DeliveredAt == nil {
		t.Fatalf("notes = %+v", ns)
	}

	owner := env.owner(t)
	for _, f := range []string{"470_agent_profile", "471_agent_traces", "472_task_steer_notes"} {
		down, err := os.ReadFile("../../../migrations/down/" + f + "_down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down %s: %v", f, err)
		}
		up, _ := migrationsFS.ReadFile("migrations/" + f + ".sql")
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down %s: %v", f, err)
		}
	}
}
