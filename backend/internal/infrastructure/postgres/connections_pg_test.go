package postgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/vault"
)

// Integration tests of migrations 240/241 (see store_rls_test.go for how to run).

func newConn(org, id string) connections.Connection {
	now := time.Now().UTC()
	return connections.Connection{ID: id, OrgID: org, Provider: "google_gmail", Kind: "oauth2", Label: "Gmail " + id, Mode: "live", Status: "active",
		Class: "read", RequestedCapabilities: []string{"mail.read"}, GrantedCapabilities: []string{"mail.read"}, ProviderScopes: []string{},
		ResourceScope: connections.ResourceScope{Labels: []string{"INBOX"}}, Limits: connections.Limits{PerDay: 100}, CreatedAt: now, UpdatedAt: now}
}

func TestConnectionsStoreRoundTripAndTenantIsolation(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	cs := &ConnStore{S: env.Store}

	a := newConn("org-a", "cn_a")
	a.OAuthClientID, a.OAuthClientSecretEnc = "client-id", []byte{9, 9}
	if err := cs.CreateConnection(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetConnection(ctx, "org-a", "cn_a")
	if err != nil || got.Label != "Gmail cn_a" || got.ResourceScope.Labels[0] != "INBOX" || got.Limits.PerDay != 100 || got.GrantedCapabilities[0] != "mail.read" || got.OAuthClientID != "client-id" || !bytes.Equal(got.OAuthClientSecretEnc, []byte{9, 9}) {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	// Another tenant cannot see or change it, even by id.
	if _, err := cs.GetConnection(ctx, "org-b", "cn_a"); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	a2 := a
	a2.OrgID, a2.Label = "org-b", "hijack"
	if err := cs.UpdateConnection(ctx, a2); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	if err := cs.CreateConnection(ctx, newConn("org-b", "cn_b")); err != nil {
		t.Fatal(err)
	}
	if l, _ := cs.ListConnections(ctx, "org-a", "", ""); len(l) != 1 || l[0].ID != "cn_a" {
		t.Fatalf("list org-a: %+v", l)
	}
	// RLS forbids writing a row of another org through the wrong tenant context.
	bad := newConn("org-a", "cn_evil")
	err = env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO connections (id, org_id, provider, kind, label) VALUES ($1,$2,'x','oauth2','x')`, bad.ID, bad.OrgID)
		return e
	})
	if err == nil {
		t.Fatal("RLS let org-b insert a row for org-a")
	}

	// grants: unique live grant per (connection, agent)
	now := time.Now().UTC()
	g := connections.Grant{ID: "g1", OrgID: "org-a", ConnectionID: "cn_a", AgentID: "sales", Capabilities: []string{"mail.read"}, MaxRisk: "low",
		RedactionProfile: "strict", IsDefault: true, Status: "active", ValidFrom: now, GrantedBy: "u1", RequestedBy: "u1", CreatedAt: now}
	if err := cs.UpsertGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	g.Status, g.ApprovedBy = "pending_second_approval", "u2"
	if err := cs.UpsertGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	dup := g
	dup.ID = "g2"
	if err := cs.UpsertGrant(ctx, dup); err == nil {
		t.Fatal("two live grants for the same (connection, agent)")
	}
	gg, err := cs.GetGrant(ctx, "org-a", "cn_a", "sales")
	if err != nil || gg.Status != "pending_second_approval" || gg.RedactionProfile != "strict" || gg.RequestedBy != "u1" {
		t.Fatalf("grant: %v %+v", err, gg)
	}
	if byAgent, _ := cs.ListGrantsByAgent(ctx, "org-b", "sales"); len(byAgent) != 0 {
		t.Fatal("grants leaked across tenants")
	}
	if n, _ := cs.RevokeGrants(ctx, "org-a", "cn_a", "admin", now); n != 1 {
		t.Fatalf("revoked %d", n)
	}
	if _, err := cs.GetGrant(ctx, "org-a", "cn_a", "sales"); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("revoked grant still live: %v", err)
	}
}

func TestUsageIsAppendOnlyAndCountsWindows(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	cs := &ConnStore{S: env.Store}
	_ = cs.CreateConnection(ctx, newConn("org-a", "cn_a"))
	now := time.Now().UTC()
	add := func(id, cap, decision, status string, ago time.Duration) {
		t.Helper()
		if err := cs.AddUsage(ctx, connections.Usage{ID: id, OrgID: "org-a", ConnectionID: "cn_a", AgentID: "sales", Tool: "email", Action: "x", Capability: cap,
			Decision: decision, Status: status, BytesIn: 10, BytesOut: 5, CostUSD: 0.25, CreatedAt: now.Add(-ago)}); err != nil {
			t.Fatal(err)
		}
	}
	add("u1", "mail.read", "allowed", "succeeded", time.Second)
	add("u2", "mail.send", "allowed", "scheduled", time.Second)
	add("u3", "mail.send", "allowed", "sent", time.Second) // the send was counted when scheduled
	add("u4", "mail.read", "denied", "skipped", time.Second)
	add("u5", "mail.read", "allowed", "succeeded", 2*time.Hour)
	n, b, err := cs.CountUsage(ctx, "org-a", "cn_a", "sales", now.Add(-time.Minute), false)
	if err != nil || n != 2 || b != 30 {
		t.Fatalf("count: %d %d %v", n, b, err)
	}
	if n, _, _ := cs.CountUsage(ctx, "org-a", "cn_a", "sales", now.Add(-time.Minute), true); n != 1 {
		t.Fatalf("writes only: %d", n)
	}
	if c, _ := cs.SumCost(ctx, "org-a", "cn_a", now.Add(-time.Hour)); c != 1.0 {
		t.Fatalf("cost %v", c)
	}
	rows, err := cs.ListUsage(ctx, "org-a", connections.UsageFilter{ConnectionID: "cn_a", Limit: 2})
	if err != nil || len(rows) != 2 {
		t.Fatalf("page 1: %v %d", err, len(rows))
	}
	more, _ := cs.ListUsage(ctx, "org-a", connections.UsageFilter{ConnectionID: "cn_a", Limit: 10, Before: rows[1].ID})
	if len(more) != 3 {
		t.Fatalf("cursor page: %d", len(more))
	}
	if den, _ := cs.ListUsage(ctx, "org-a", connections.UsageFilter{Result: "denied"}); len(den) != 1 {
		t.Fatalf("filter: %d", len(den))
	}
	// append-only for the application role
	if err := env.Store.exec(ctx, "org-a", `UPDATE connection_usage SET status='failed' WHERE id='u1'`); err == nil {
		t.Fatal("the application role could UPDATE connection_usage")
	}
	if err := env.Store.exec(ctx, "org-a", `DELETE FROM connection_usage WHERE id='u1'`); err == nil {
		t.Fatal("the application role could DELETE from connection_usage")
	}
}

func TestOAuthStateIsSingleUseAndConsumableWithoutOrg(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	cs := &ConnStore{S: env.Store}
	_ = cs.CreateConnection(ctx, newConn("org-a", "cn_a"))
	now := time.Now().UTC()
	st := connections.OAuthState{StateHash: "hash-1", OrgID: "org-a", ConnectionID: "cn_a", UserID: "u1", VerifierEnc: []byte{1, 2, 3},
		RedirectURI: "http://x/cb", Capabilities: []string{"mail.read"}, ExpiresAt: now.Add(10 * time.Minute)}
	if err := cs.PutOAuthState(ctx, st); err != nil {
		t.Fatal(err)
	}
	// The public callback has no org: the state hash alone opens the row.
	got, err := cs.ConsumeOAuthState(ctx, "hash-1", now)
	if err != nil || got.OrgID != "org-a" || !bytes.Equal(got.VerifierEnc, []byte{1, 2, 3}) {
		t.Fatalf("consume: %v %+v", err, got)
	}
	if _, err := cs.ConsumeOAuthState(ctx, "hash-1", now); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("state reused: %v", err)
	}
	st.StateHash = "hash-2"
	st.ExpiresAt = now.Add(-time.Second)
	_ = cs.PutOAuthState(ctx, st)
	if _, err := cs.ConsumeOAuthState(ctx, "hash-2", now); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("expired state accepted: %v", err)
	}
	if _, err := cs.ConsumeOAuthState(ctx, "guess", now); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("unknown state: %v", err)
	}
	// Without presenting the hash the table is invisible across tenants.
	seedOrg(t, env.Store, "org-b")
	st.StateHash, st.ExpiresAt = "hash-3", now.Add(time.Minute)
	_ = cs.PutOAuthState(ctx, st)
	var n int
	_ = env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM oauth_states`).Scan(&n)
	})
	if n != 0 {
		t.Fatalf("org-b sees %d oauth states of org-a", n)
	}
}

func TestVaultOnPostgresEncryptsRotatesAndShreds(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	cs := &ConnStore{S: env.Store}
	_ = cs.CreateConnection(ctx, newConn("org-a", "cn_a"))
	kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	v := vault.New(&VaultRepo{S: env.Store}, kw)

	const secret = "CANARY_SECRET_9f2c_abcdefghijklmnop"
	m, err := v.Put(ctx, "org-a", "cn_a", vault.KindAPIKey, vault.SecretFromString(secret), "u1", nil)
	if err != nil || m.Version != 1 || m.Hint != "mnop" {
		t.Fatalf("put: %v %+v", err, m)
	}
	// plaintext never reaches the table
	var found bool
	_ = env.Store.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credentials WHERE position($1::bytea in ciphertext) > 0)`, []byte(secret)).Scan(&found)
	})
	if found {
		t.Fatal("plaintext secret stored in credentials.ciphertext")
	}
	var got string
	if err := v.Use(ctx, "org-a", "cn_a", func(s vault.Secret) error { got = string(s.Reveal()); return nil }); err != nil || got != secret {
		t.Fatalf("use: %v", err)
	}
	// another tenant cannot read it
	if err := v.Use(ctx, "org-b", "cn_a", func(vault.Secret) error { return nil }); !errors.Is(err, vault.ErrNotFound) {
		t.Fatalf("cross-tenant use: %v", err)
	}
	// rotation keeps exactly one active version
	if m, err = v.Put(ctx, "org-a", "cn_a", vault.KindAPIKey, vault.SecretFromString("rotated-rotated-rotated-0"), "u1", nil); err != nil || m.Version != 2 {
		t.Fatalf("rotate: %v %+v", err, m)
	}
	if n, err := v.RotateDEK(ctx, "org-a"); err != nil || n != 2 {
		t.Fatalf("rotate dek: %d %v", n, err)
	}
	if err := v.Use(ctx, "org-a", "cn_a", func(s vault.Secret) error { got = string(s.Reveal()); return nil }); err != nil || got != "rotated-rotated-rotated-0" {
		t.Fatalf("use after DEK rotation: %v %q", err, got)
	}
	// crypto-shred
	if err := v.Destroy(ctx, "org-a", "cn_a"); err != nil {
		t.Fatal(err)
	}
	if err := v.Use(ctx, "org-a", "cn_a", func(vault.Secret) error { return nil }); !errors.Is(err, vault.ErrDestroyed) {
		t.Fatalf("after destroy: %v", err)
	}
	var live int
	_ = env.Store.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM credentials WHERE ciphertext IS NOT NULL`).Scan(&live)
	})
	if live != 0 {
		t.Fatalf("%d ciphertexts survived the shred", live)
	}
	// sealed data (PKCE verifier) round trip
	sealed, err := v.Seal(ctx, "org-a", "pkce:x", []byte("verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := v.Unseal(ctx, "org-a", "pkce:x", sealed); err != nil || string(plain) != "verifier" {
		t.Fatalf("unseal: %v", err)
	}
	if _, err := v.Unseal(ctx, "org-a", "pkce:other", sealed); err == nil {
		t.Fatal("sealed data must be bound to its purpose")
	}
}

func TestHoldsAndControlsOnPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	cs := &ConnStore{S: env.Store, ExtraOrgs: []string{"org-a", "org-b"}}
	now := time.Now().UTC()
	mk := func(org, id string, until time.Time) connections.Hold {
		return connections.Hold{ID: id, OrgID: org, ConnectionID: "cn", AgentID: "sales", Tool: "email", Action: "send", Capability: "mail.send",
			Payload: map[string]any{"to": "x@y.com", "body": "hola"}, Summary: "s", Recipients: []string{"x@y.com"}, Status: "held", HoldUntil: until, CreatedAt: now}
	}
	_ = cs.PutHold(ctx, mk("org-a", "h1", now.Add(-time.Second)))
	_ = cs.PutHold(ctx, mk("org-b", "h2", now.Add(-time.Second)))
	_ = cs.PutHold(ctx, mk("org-a", "h3", now.Add(time.Minute)))
	due, err := cs.ListDueHolds(ctx, now)
	if err != nil || len(due) != 2 {
		t.Fatalf("due across orgs: %v %d", err, len(due))
	}
	h, _ := cs.GetHold(ctx, "org-a", "h1")
	if h.Payload["body"] != "hola" || h.Recipients[0] != "x@y.com" {
		t.Fatalf("hold: %+v", h)
	}
	h.Status, h.Payload = "cancelled", nil
	if err := cs.UpdateHold(ctx, h); err != nil {
		t.Fatal(err)
	}
	if h2, _ := cs.GetHold(ctx, "org-a", "h1"); h2.Status != "cancelled" || len(h2.Payload) != 0 {
		t.Fatalf("payload kept after decision: %+v", h2)
	}
	if _, err := cs.GetHold(ctx, "org-b", "h1"); !errors.Is(err, connections.ErrNotFound) {
		t.Fatal("hold visible across tenants")
	}

	// controls
	st, err := cs.GetOrg(ctx, "org-a")
	if err != nil || st.Mode != "normal" || st.KillSwitchLevel != "none" {
		t.Fatalf("default controls: %v %+v", err, st)
	}
	at := now
	if err := cs.PutOrg(ctx, "org-a", controls.OrgState{Mode: "read_only", KillSwitchLevel: "freeze", Reason: "x", SetBy: "u", SetAt: &at,
		Settings: controls.Settings{PlanReview: "always"}, DisabledTools: []string{"email.send"}}); err != nil {
		t.Fatal(err)
	}
	if st, _ = cs.GetOrg(ctx, "org-a"); st.KillSwitchLevel != "freeze" || st.Settings.PlanReview != "always" || st.DisabledTools[0] != "email.send" {
		t.Fatalf("controls: %+v", st)
	}
	if st, _ = cs.GetOrg(ctx, "org-b"); st.KillSwitchLevel != "none" {
		t.Fatal("kill switch leaked to another tenant")
	}
	if err := cs.PutAgent(ctx, "org-a", controls.AgentControl{AgentID: "sales", Control: "paused", Drain: "graceful", PausedBy: "u", PausedAt: &at, PauseReason: "r"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := cs.GetAgent(ctx, "org-a", "sales"); a.Control != "paused" || a.Drain != "graceful" {
		t.Fatalf("agent control: %+v", a)
	}
	if a, _ := cs.GetAgent(ctx, "org-b", "sales"); a.Control != "active" {
		t.Fatal("pause leaked to another tenant")
	}
}
