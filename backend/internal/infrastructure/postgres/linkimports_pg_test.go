package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/linkimports"
)

// Integration test of migration 804 (link imports). Needs TEST_DATABASE_URL (see store_rls_test.go); skipped
// otherwise.
func TestLinkImportsPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	st := &LinkImportStore{S: env.Store}
	now := time.Now().UTC().Truncate(time.Microsecond)

	li := linkimports.LinkImport{ID: "lnk_1", Name: "Ventas", Slug: "ventas", URL: "https://sheets.example.com/pub?output=csv",
		Folder: "/Shared/Imports/Links/ventas", Schedule: "hourly", Enabled: true, HeaderName: "X-Api-Key", HeaderSealed: []byte{0, 0, 0, 1, 9, 9},
		HeaderHint: "abcd", CreatedBy: "u1", CreatedAt: now, UpdatedAt: now, NextRunAt: &now}
	if err := st.Create(ctx, "org-a", li); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "org-a", li); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate id: %v", err)
	}
	got, err := st.Get(ctx, "org-a", "lnk_1")
	if err != nil || got.URL != li.URL || len(got.HeaderSealed) != 6 || got.NextRunAt == nil || !got.NextRunAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.Get(ctx, "org-b", "lnk_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS: org-b must not see org-a's import: %v", err)
	}
	if l, _ := st.List(ctx, "org-b"); len(l) != 0 {
		t.Fatal("RLS: org-b lists nothing")
	}
	if d, _ := st.Due(ctx, "org-b", now.Add(time.Hour)); len(d) != 0 {
		t.Fatal("RLS: org-b has nothing due")
	}
	if err := st.Delete(ctx, "org-b", "lnk_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS: org-b cannot delete it: %v", err)
	}

	// scheduling: due + compare-and-set claim
	due, err := st.Due(ctx, "org-a", now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due: %v %v", due, err)
	}
	next := now.Add(time.Hour)
	if ok, err := st.Claim(ctx, "org-a", "lnk_1", now, next); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, _ := st.Claim(ctx, "org-a", "lnk_1", now, next); ok {
		t.Fatal("a period is claimed once")
	}
	if ok, _ := st.Claim(ctx, "org-b", "lnk_1", next, next.Add(time.Hour)); ok {
		t.Fatal("RLS: org-b cannot claim it")
	}

	// runs
	r1 := linkimports.Run{ID: "lnr_1", ImportID: "lnk_1", Trigger: "schedule", Status: "ok", StartedAt: now, Size: 12, Hash: "h1", FileID: "fil_1",
		Path: "/Shared/Imports/Links/ventas/ventas.csv", Version: 1, Mime: "text/csv"}
	if err := st.RecordRun(ctx, "org-a", r1); err != nil {
		t.Fatal(err)
	}
	r2 := linkimports.Run{ID: "lnr_2", ImportID: "lnk_1", Trigger: "manual", Actor: "u1", Status: "error", Code: "http_status", HTTPStatus: 503,
		StartedAt: now.Add(time.Minute)}
	if err := st.RecordRun(ctx, "org-a", r2); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRun(ctx, "org-b", linkimports.Run{ID: "lnr_x", ImportID: "lnk_1", Trigger: "manual", Status: "ok", StartedAt: now}); err == nil {
		t.Fatal("RLS: org-b cannot record a run on org-a's import")
	}
	got, _ = st.Get(ctx, "org-a", "lnk_1")
	if got.LastStatus != "error" || got.LastCode != "http_status" || got.LastHash != "h1" || got.LastFileID != "fil_1" || got.RunCount != 2 ||
		got.LastDataAt == nil || !got.LastDataAt.Equal(now) {
		t.Fatalf("summary after an error keeps the last good file: %+v", got)
	}
	runs, err := st.Runs(ctx, "org-a", "lnk_1", 10)
	if err != nil || len(runs) != 2 || runs[0].ID != "lnr_2" || runs[0].HTTPStatus != 503 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	if rs, _ := st.Runs(ctx, "org-b", "lnk_1", 10); len(rs) != 0 {
		t.Fatal("RLS: org-b sees no runs")
	}

	// update clears the header; delete removes the runs
	got.HeaderName, got.HeaderSealed, got.HeaderHint, got.Enabled, got.UpdatedAt = "", nil, "", false, now
	if err := st.Update(ctx, "org-a", got); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, "org-a", "lnk_1")
	if got.HeaderSealed != nil || got.Enabled {
		t.Fatalf("update: %+v", got)
	}
	if err := st.Delete(ctx, "org-a", "lnk_1"); err != nil {
		t.Fatal(err)
	}
	if rs, _ := st.Runs(ctx, "org-a", "lnk_1", 10); len(rs) != 0 {
		t.Fatal("runs go with the import")
	}
}
