package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/portal"
)

// Integration test of migration 798 (client portal). Needs TEST_DATABASE_URL (see store_rls_test.go);
// skipped otherwise.
func TestPortalPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	st := &PortalStore{S: env.Store}
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Branding: defaults, upsert, per organization.
	if _, err := st.GetBranding(ctx, "org-a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no branding yet: %v", err)
	}
	b := portal.Branding{DisplayName: "Despacho Pérez", AccentColor: "#0b5fff", Logo: []byte{0x89, 'P', 'N', 'G'}, LogoMime: "image/png", UpdatedBy: "u1", UpdatedAt: now}
	if err := st.PutBranding(ctx, "org-a", b); err != nil {
		t.Fatal(err)
	}
	b.DisplayName = "Despacho Pérez y Asociados"
	if err := st.PutBranding(ctx, "org-a", b); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetBranding(ctx, "org-a")
	if err != nil || got.DisplayName != "Despacho Pérez y Asociados" || len(got.Logo) != 4 || got.LogoMime != "image/png" {
		t.Fatalf("branding: %+v %v", got, err)
	}
	if _, err := st.GetBranding(ctx, "org-b"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS: org-b must not see org-a's branding: %v", err)
	}
	if err := st.PutBranding(ctx, "org-a", portal.Branding{AccentColor: "red;", UpdatedAt: now}); err == nil {
		t.Fatal("the database refuses a malformed color too")
	}
	got.Logo, got.LogoMime = nil, ""
	if err := st.PutBranding(ctx, "org-a", got); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.GetBranding(ctx, "org-a"); len(g.Logo) != 0 {
		t.Fatal("logo removed")
	}

	// Links.
	hash := portal.HashToken("token-of-org-a")
	exp := now.Add(48 * time.Hour)
	l := portal.Link{ID: "pl_1", ProjectID: "p1", Label: "Cliente", DeliverableIDs: []string{"a1", "a2"}, TokenHash: hash, CreatedBy: "u1", CreatedAt: now, UpdatedAt: now, ExpiresAt: &exp}
	if err := st.CreateLink(ctx, "org-a", l); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateLink(ctx, "org-b", portal.Link{ID: "pl_x", ProjectID: "p1", TokenHash: hash, CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("token hashes are unique across organizations")
	}
	if _, err := st.GetLink(ctx, "org-b", "pl_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RLS: %v", err)
	}
	if ls, _ := st.ListLinks(ctx, "org-b", "p1"); len(ls) != 0 {
		t.Fatalf("RLS list: %v", ls)
	}
	org, byHash, err := st.LinkByHash(ctx, hash)
	if err != nil || org != "org-a" || byHash.ID != "pl_1" || len(byHash.DeliverableIDs) != 2 || byHash.ExpiresAt == nil || !byHash.ExpiresAt.Equal(exp) {
		t.Fatalf("lookup: %s %+v %v", org, byHash, err)
	}
	if _, _, err := st.LinkByHash(ctx, portal.HashToken("unknown")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}
	// The lookup policy shows ONE row and is read-only: without the hash nothing is visible, and the
	// announced hash cannot be used to write.
	err = pgx.BeginFunc(ctx, env.Store.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM portal_links`).Scan(&n); err != nil || n != 0 {
			t.Errorf("no tenant, no hash: %d rows (%v)", n, err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.portal_hash', $1, true)`, hash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM portal_links`).Scan(&n); err != nil || n != 1 {
			t.Errorf("hash announced: %d rows (%v)", n, err)
		}
		tag, err := tx.Exec(ctx, `UPDATE portal_links SET label='hacked' WHERE token_hash=$1`, hash)
		if err == nil && tag.RowsAffected() != 0 {
			t.Error("the lookup policy must not allow writes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	l.Label, l.Title, l.DeliverableIDs, l.UpdatedAt = "Cliente López", "Cierre de octubre", []string{"a3"}, now.Add(time.Minute)
	if err := st.UpdateLink(ctx, "org-a", l); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateLink(ctx, "org-b", l); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update from org-b: %v", err)
	}
	first, err := st.MarkViewed(ctx, "org-a", "pl_1", now)
	if err != nil || !first {
		t.Fatalf("first view: %v %v", first, err)
	}
	if first, _ := st.MarkViewed(ctx, "org-a", "pl_1", now.Add(10*time.Second)); first {
		t.Fatal("a second view is not the first")
	}
	if first, _ := st.MarkViewed(ctx, "org-a", "pl_1", now.Add(2*time.Minute)); first {
		t.Fatal("still not the first")
	}
	g, _ := st.GetLink(ctx, "org-a", "pl_1")
	if g.Label != "Cliente López" || g.Title != "Cierre de octubre" || len(g.DeliverableIDs) != 1 || g.FirstViewedAt == nil || !g.FirstViewedAt.Equal(now) || g.LastViewedAt == nil || !g.LastViewedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("after views: %+v", g)
	}
	if err := st.RevokeLink(ctx, "org-b", "pl_1", "x", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoke from org-b: %v", err)
	}
	if err := st.RevokeLink(ctx, "org-a", "pl_1", "u2", now); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeLink(ctx, "org-a", "pl_1", "u3", now.Add(time.Hour)); err != nil {
		t.Fatal("idempotent")
	}
	if g, _ := st.GetLink(ctx, "org-a", "pl_1"); g.RevokedAt == nil || g.RevokedBy != "u2" {
		t.Fatalf("revoked: %+v", g)
	}
	err = env.Store.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM portal_links WHERE org_id='org-a'`)
		return err
	})
	if err == nil {
		t.Fatal("the app role cannot delete links (history is kept)")
	}

	// Rollback leaves the schema clean.
	owner, err := pgx.Connect(ctx, env.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	down, err := os.ReadFile("../../../migrations/down/798_client_portal_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var c int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name IN ('portal_links','portal_branding')`).Scan(&c); err != nil || c != 0 {
		t.Fatalf("tables still there: %d %v", c, err)
	}
}
