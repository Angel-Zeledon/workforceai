package connections

import (
	"bytes"
	"context"
	"encoding/base64"
	"sync"
	"testing"

	"aiworkforce/backend/internal/vault"
)

type emitted struct {
	typ     string
	payload map[string]any
}

// D3 (minor): every live event of a connection names its office, so the audience rule (application.RefsOf,
// internal/officeaudience) keeps the events of a private department's connection inside it.
func TestConnectionEventsNameTheirOffice(t *testing.T) {
	ctx := context.Background()
	kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	var mu sync.Mutex
	var evs []emitted
	s, err := NewService(Config{Store: NewMemStore(), Vault: vault.New(vault.NewMemRepo(), kw),
		Emit: func(_ context.Context, _, typ string, p map[string]any) {
			mu.Lock()
			evs = append(evs, emitted{typ, p})
			mu.Unlock()
		}})
	if err != nil {
		t.Fatal(err)
	}
	sec := vault.SecretFromString("sk-test-0123456789")
	c, err := s.Create(ctx, "org1", CreateInput{Provider: SourceProvider, Kind: "api_key", Label: "Contratos API", Mode: ModeLive, Actor: "u1",
		Secret: &sec, Source: &Source{BaseURL: "https://api.example.test", Auth: SourceAuth{Style: "bearer"}}, OfficeID: "legal"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Create(ctx, "org1", CreateInput{Provider: "google_gmail", Label: "Correo", Mode: ModeSimulated, Actor: "u1", OfficeID: "general"})
	if err != nil {
		t.Fatal(err)
	}
	evs = nil
	for _, id := range []string{c.ID, g.ID} {
		if _, err := s.Test(ctx, "org1", id, "u1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Suspend(ctx, "org1", id, "u1", "manual"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, "org1", id, "u1"); err != nil {
			t.Fatal(err)
		}
	}
	s.MarkNeedsReauth(ctx, "org1", c.ID)
	if _, err := s.RotateCredential(ctx, "org1", c.ID, vault.SecretFromString("sk-test-9876543210"), "u1", false); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ id, label string }{{c.ID, c.Label}, {g.ID, g.Label}} {
		if _, err := s.Revoke(ctx, "org1", x.id, x.label, "u1"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, e := range evs {
		id, _ := e.payload["connection_id"].(string)
		want := "legal"
		if id == g.ID {
			want = ""
		}
		got, ok := e.payload["office_id"]
		if !ok || got != want {
			t.Errorf("%s of %s: office_id = %v (present %v), want %q", e.typ, id, got, ok, want)
		}
		seen[e.typ] = true
	}
	for _, typ := range []string{"connection.status_changed", "connection.tested", "connection.revoked", "connection.rotated"} {
		if !seen[typ] {
			t.Errorf("no %s emitted", typ)
		}
	}
}
