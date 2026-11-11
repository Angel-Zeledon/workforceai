package connections

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/vault"
)

// D3 (pendientes, área B, punto 6): the lifecycle of a connection.

// blockingProvider answers Test only when release is closed (a slow provider check).
type blockingProvider struct {
	Provider
	started chan struct{}
	release chan struct{}
}

func (p blockingProvider) Test(context.Context, Handle) TestResult {
	close(p.started)
	<-p.release
	return TestResult{OK: true}
}

type auditRec struct {
	mu      sync.Mutex
	actions []string
}

func (a *auditRec) audit(_ context.Context, _, _, action, _, _ string, _ map[string]any) {
	a.mu.Lock()
	a.actions = append(a.actions, action)
	a.mu.Unlock()
}

func (a *auditRec) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, x := range a.actions {
		if x == action {
			return true
		}
	}
	return false
}

// failingUpdate is a store whose UpdateConnection fails (the database went away).
type failingUpdate struct {
	*MemStore
	fail bool
}

func (f *failingUpdate) UpdateConnection(ctx context.Context, c Connection) error {
	if f.fail {
		return errors.New("database unavailable")
	}
	return f.MemStore.UpdateConnection(ctx, c)
}

func newPendService(t *testing.T, store Store, providers map[string]Provider, au *auditRec) *Service {
	t.Helper()
	kw, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	cfg := Config{Store: store, Vault: vault.New(vault.NewMemRepo(), kw), Providers: providers}
	if au != nil {
		cfg.Audit = au.audit
	}
	s, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func liveSource(t *testing.T, s *Service) Connection {
	t.Helper()
	sec := vault.SecretFromString("sk-test-0123456789")
	c, err := s.Create(context.Background(), "org1", CreateInput{Provider: SourceProvider, Kind: "api_key", Label: "Contratos API", Mode: ModeLive,
		Actor: "u1", Secret: &sec, Source: &Source{BaseURL: "https://api.example.test", Auth: SourceAuth{Style: "bearer"}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A test of the connection takes a while (the provider is called); what an admin changed meanwhile (suspended it) is
// not overwritten when the result is written.
func TestTestKeepsWhatChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	bp := blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	s := newPendService(t, NewMemStore(), map[string]Provider{SourceProvider: bp}, nil)
	c := liveSource(t, s)
	done := make(chan error, 1)
	go func() { _, err := s.Test(ctx, "org1", c.ID, "u1"); done <- err }()
	<-bp.started
	if _, err := s.Suspend(ctx, "org1", c.ID, "u2", "manual"); err != nil {
		t.Fatal(err)
	}
	close(bp.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, _ := s.Store.GetConnection(ctx, "org1", c.ID)
	if got.Status != StatusSuspended || got.LastTestStatus != "ok" || got.LastTestedAt == nil {
		t.Fatalf("after a test during a suspension: status=%s test=%s", got.Status, got.LastTestStatus)
	}
}

// A new secret through PATCH revives an expired or failing connection, like rotating it does.
func TestPatchWithSecretRevivesTheConnection(t *testing.T) {
	ctx := context.Background()
	for _, st := range []string{StatusExpired, StatusError} {
		s := newPendService(t, NewMemStore(), nil, nil)
		c := liveSource(t, s)
		c.Status, c.StatusReason = st, "token_expired"
		if err := s.Store.UpdateConnection(ctx, c); err != nil {
			t.Fatal(err)
		}
		sec := vault.SecretFromString("sk-test-9876543210")
		got, err := s.Patch(ctx, "org1", c.ID, PatchInput{Secret: &sec, Actor: "u1"})
		if err != nil || got.Status != StatusActive || got.StatusReason != "" {
			t.Fatalf("%s + new secret: %v %s %q", st, err, got.Status, got.StatusReason)
		}
	}
}

// The secret was replaced in the vault but the connection could not be saved: the replacement is still audited.
func TestPatchSecretIsAuditedWhenTheSaveFails(t *testing.T) {
	ctx := context.Background()
	au := &auditRec{}
	store := &failingUpdate{MemStore: NewMemStore()}
	s := newPendService(t, store, nil, au)
	c := liveSource(t, s)
	store.fail = true
	sec := vault.SecretFromString("sk-test-9876543210")
	if _, err := s.Patch(ctx, "org1", c.ID, PatchInput{Secret: &sec, Actor: "u1"}); err == nil {
		t.Fatal("the save failed but Patch answered ok")
	}
	if !au.has("connection.secret_replaced") {
		t.Fatalf("the vault write left no audit: %v", au.actions)
	}
}

// Weakening the TLS of a database (sslmode) changes how its password travels: it needs the password again, like
// changing its host.
func TestSSLModeIsPartOfWhereTheSecretGoes(t *testing.T) {
	ctx := context.Background()
	s := newPendService(t, NewMemStore(), nil, nil)
	sec := vault.SecretFromString("contraseña-larga-123")
	c, err := s.Create(ctx, "org1", CreateInput{Provider: DatabaseProvider, Kind: "api_key", Label: "Ventas DB", Mode: ModeLive, Actor: "u1",
		Secret: &sec, Database: &Database{Host: "db.example.test", User: "reader", Name: "ventas", SSLMode: "verify-full"}})
	if err != nil {
		t.Fatal(err)
	}
	d := *c.Database
	d.SSLMode = "disable"
	_, err = s.Patch(ctx, "org1", c.ID, PatchInput{Database: &d, SecretToMove: true, Actor: "u2"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), CodeCredentialRequired) {
		t.Fatalf("sslmode disable without the password again: %v", err)
	}
}

// When the office of an agent cannot be told, granting (or approving a grant on) an office's connection fails
// closed with its own error, not as if the agent were of another office.
func TestGrantWhenTheOfficeCannotBeTold(t *testing.T) {
	ctx := context.Background()
	s := newPendService(t, NewMemStore(), nil, nil)
	sec := vault.SecretFromString("sk-test-0123456789")
	c, err := s.Create(ctx, "org1", CreateInput{Provider: SourceProvider, Kind: "api_key", Label: "Contratos API", Mode: ModeLive, Actor: "u1",
		Secret: &sec, Source: &Source{BaseURL: "https://api.example.test", Auth: SourceAuth{Style: "bearer"}}, OfficeID: "legal"})
	if err != nil {
		t.Fatal(err)
	}
	s.AgentOffice = func(context.Context, string, string) (string, error) { return "", errors.New("offices unavailable") }
	_, err = s.PutGrant(ctx, "org1", c.ID, "legal-agent", GrantInput{Capabilities: []string{"api.read"}}, "u1")
	if !errors.Is(err, ErrOfficeUnknown) || strings.Contains(err.Error(), CodeAgentOtherOffice) {
		t.Fatalf("office unavailable: %v", err)
	}
	s.AgentOffice = nil // no offices wired: an office connection cannot be granted either
	if _, err := s.PutGrant(ctx, "org1", c.ID, "legal-agent", GrantInput{Capabilities: []string{"api.read"}}, "u1"); !errors.Is(err, ErrOfficeUnknown) {
		t.Fatalf("without offices: %v", err)
	}
}
