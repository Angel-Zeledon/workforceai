package postgres

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
)

// Schedule holds (migration 760): round trip, upsert keeps created_at, ordering and tenant isolation under RLS.
func TestHoldStorePostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	hs := &HoldStore{S: st}
	ctx := context.Background()
	seedOrg(t, st, "org-h1")
	seedOrg(t, st, "org-h2")
	at := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	if err := hs.PutHold(ctx, "org-h1", application.Hold{TaskID: "t2", RequestID: "r", AgentID: "sales", Reason: "closed", WakeAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := hs.PutHold(ctx, "org-h1", application.Hold{TaskID: "t1", RequestID: "r", WakeAt: at}); err != nil {
		t.Fatal(err)
	}
	list, err := hs.ListHolds(ctx, "org-h1")
	if err != nil || len(list) != 2 || list[0].TaskID != "t1" || list[1].Reason != "closed" || list[1].CreatedAt.IsZero() {
		t.Fatalf("holds = %+v %v", list, err)
	}
	first := list[1].CreatedAt
	if err := hs.PutHold(ctx, "org-h1", application.Hold{TaskID: "t2", RequestID: "r", WakeAt: at.Add(2 * time.Hour), CreatedAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if list, _ = hs.ListHolds(ctx, "org-h1"); !list[1].CreatedAt.Equal(first) || !list[1].WakeAt.Equal(at.Add(2*time.Hour)) {
		t.Fatalf("upsert must keep created_at and move wake_at: %+v", list)
	}
	if other, _ := hs.ListHolds(ctx, "org-h2"); len(other) != 0 {
		t.Fatal("org-h2 sees holds of org-h1")
	}
	if err := hs.DeleteHold(ctx, "org-h1", "t1"); err != nil {
		t.Fatal(err)
	}
	if err := hs.DeleteHold(ctx, "org-h1", "missing"); err != nil {
		t.Fatalf("deleting a missing hold is not an error: %v", err)
	}
	if list, _ = hs.ListHolds(ctx, "org-h1"); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
}
