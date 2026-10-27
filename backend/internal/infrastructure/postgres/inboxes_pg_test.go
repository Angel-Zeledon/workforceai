package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/inboxes"
)

// Integration test of migration 803 (data inboxes). Needs TEST_DATABASE_URL (see store_rls_test.go); skipped
// otherwise.
func TestInboxesPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	st := &InboxStore{S: env.Store}
	now := time.Now().UTC().Truncate(time.Microsecond)

	hash := inboxes.HashToken("aiwi_token-of-org-a")
	i := inboxes.Inbox{ID: "ibx_1", Name: "Pedidos", Slug: "pedidos", Folder: "/Shared/Imports/pedidos", Allowed: []string{"json", "csv"},
		MaxBytes: 1 << 20, RatePerMin: 30, TokenHash: hash, TokenHint: "abcd", CreatedBy: "u1", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateInbox(ctx, "org-a", i); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateInbox(ctx, "org-b", inboxes.Inbox{ID: "ibx_2", Name: "x", Slug: "x", Folder: "/Shared/x", MaxBytes: 1, RatePerMin: 1,
		TokenHash: hash, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("token hashes are globally unique: %v", err)
	}
	got, err := st.GetInbox(ctx, "org-a", "ibx_1")
	if err != nil || got.Name != "Pedidos" || len(got.Allowed) != 2 || got.TokenHint != "abcd" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetInbox(ctx, "org-b", "ibx_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS: org-b must not see org-a's inbox: %v", err)
	}
	if l, _ := st.ListInboxes(ctx, "org-b"); len(l) != 0 {
		t.Fatal("RLS: org-b lists nothing")
	}
	org, byHash, err := st.InboxByHash(ctx, hash)
	if err != nil || org != "org-a" || byHash.ID != "ibx_1" {
		t.Fatalf("lookup: %q %+v %v", org, byHash, err)
	}
	if _, _, err := st.InboxByHash(ctx, inboxes.HashToken("other")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}

	// pushes and idempotency
	p := inboxes.Push{ID: "psh_1", InboxID: "ibx_1", IdemKey: "k1", Size: 10, ReceivedAt: now}
	if err := st.ReservePush(ctx, "org-a", p); err != nil {
		t.Fatal(err)
	}
	if err := st.ReservePush(ctx, "org-a", inboxes.Push{ID: "psh_2", InboxID: "ibx_1", IdemKey: "k1", ReceivedAt: now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate key: %v", err)
	}
	p.Files = []inboxes.PushedFile{{FileID: "fil_1", Path: "/Shared/Imports/pedidos/a.json", Name: "a.json", Version: 1, Size: 10, Kind: "json", Created: true}}
	if err := st.CompletePush(ctx, "org-a", p); err != nil {
		t.Fatal(err)
	}
	k, err := st.PushByKey(ctx, "org-a", "ibx_1", "k1")
	if err != nil || k.Status != inboxes.PushDone || len(k.Files) != 1 || k.Files[0].FileID != "fil_1" {
		t.Fatalf("by key: %+v %v", k, err)
	}
	if _, err := st.PushByKey(ctx, "org-b", "ibx_1", "k1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS on pushes: %v", err)
	}
	if err := st.ReservePush(ctx, "org-a", inboxes.Push{ID: "psh_3", InboxID: "ibx_1", ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReleasePush(ctx, "org-a", "ibx_1", "psh_3"); err != nil {
		t.Fatal(err)
	}
	ps, err := st.Pushes(ctx, "org-a", "ibx_1", 10)
	if err != nil || len(ps) != 1 {
		t.Fatalf("pushes: %+v %v", ps, err)
	}
	got, _ = st.GetInbox(ctx, "org-a", "ibx_1")
	if got.PushCount != 1 || got.LastPushAt == nil {
		t.Fatalf("counters: %+v", got)
	}

	// rotate and revoke
	if err := st.SetToken(ctx, "org-a", "ibx_1", inboxes.HashToken("aiwi_new"), "wxyz", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InboxByHash(ctx, hash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("the old hash is gone")
	}
	if err := st.RevokeInbox(ctx, "org-b", "ibx_1", "u", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS on revoke: %v", err)
	}
	if err := st.RevokeInbox(ctx, "org-a", "ibx_1", "u1", now); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetInbox(ctx, "org-a", "ibx_1")
	if got.RevokedAt == nil || got.RevokedBy != "u1" {
		t.Fatalf("revoked: %+v", got)
	}
}
