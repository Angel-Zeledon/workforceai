package postgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/vault"
)

const pgTestKey = "sk-proj-PGTESTONLY-abcdefghijklmnop-1234567890"

// Bring-your-own-model (migration 580): the key is stored only as ciphertext under the organization's data
// key, rows are tenant-isolated by RLS, and the application can read it back through the service.
func TestLLMCredentialsEncryptedAtRestAndIsolated(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	kw, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	svc := &application.LLMService{Store: st, Sealer: vault.New(&VaultRepo{S: st}, kw), Cfg: application.Config{OrgID: "org-a"}, TTL: time.Nanosecond}
	ctxA, ctxB := application.WithOrg(ctx, "org-a"), application.WithOrg(ctx, "org-b")

	if _, err := svc.Set(ctxA, "openai", application.LLMInput{APIKey: pgTestKey, Model: "gpt-4.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetActive(ctxA, "openai"); err != nil {
		t.Fatal(err)
	}

	// The raw table holds ciphertext: the clear key (and any fragment of it beyond the last 4) is nowhere.
	owner := env.owner(t)
	var sealed []byte
	var last4 string
	if err := owner.QueryRow(ctx, `SELECT sealed_key, key_last4 FROM org_llm_credentials WHERE org_id='org-a' AND provider='openai'`).Scan(&sealed, &last4); err != nil {
		t.Fatal(err)
	}
	if len(sealed) < 30 || bytes.Contains(sealed, []byte("PGTESTONLY")) || bytes.Contains(sealed, []byte("sk-proj")) || last4 != "7890" {
		t.Fatalf("sealed key is not ciphertext or last4 wrong: len=%d last4=%q", len(sealed), last4)
	}
	var dump string
	_ = owner.QueryRow(ctx, `SELECT coalesce(string_agg(t::text, ' '), '') FROM org_llm_credentials t`).Scan(&dump)
	if bytes.Contains([]byte(dump), []byte("PGTESTONLY")) {
		t.Fatal("the clear key is in the table dump")
	}

	// The service decrypts it for the runtime call.
	p := svc.Apply(ctxA, application.RuntimePolicy{})
	if len(p.ProviderCredentials) != 1 || p.ProviderCredentials[0].APIKey != pgTestKey || !p.ByoOnly {
		t.Fatalf("apply = %+v", p)
	}
	// A different organization gets nothing: not the credential, not the choice.
	if p := svc.Apply(ctxB, application.RuntimePolicy{}); !p.Empty() {
		t.Fatalf("org-b sees org-a's choice: %+v", p)
	}
	if l, _ := st.ListLLMCredentials(ctx, "org-b"); len(l) != 0 {
		t.Fatal("org-b lists org-a credentials")
	}
	if a, _ := st.GetLLMActive(ctx, "org-b"); a != "" {
		t.Fatalf("org-b active = %q", a)
	}
	// The sealed blob is bound to its organization and provider: moved elsewhere it does not open.
	rec, _ := st.ListLLMCredentials(ctx, "org-a")
	if _, err := svc.Sealer.Unseal(ctx, "org-a", "llm_key:kimi", rec[0].Sealed); err == nil {
		t.Error("a sealed key opened under another provider's purpose")
	}

	// RLS: org-b's transaction cannot read or write org-a rows.
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_llm_credentials WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b transaction sees %d credentials of org-a", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO org_llm_credentials (org_id, provider, model, sealed_key) VALUES ('org-a','kimi','m','\x00')`); err == nil {
		t.Error("org-b wrote a credential for org-a")
	}
	if err := st.exec(ctx, "org-b", `DELETE FROM org_llm_credentials WHERE org_id='org-a'`); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListLLMCredentials(ctx, "org-a"); len(l) != 1 {
		t.Fatal("org-b deleted an org-a credential")
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO org_llm_credentials (org_id, provider, model, sealed_key) VALUES ('org-b','mistral','m','\x00')`); err == nil {
		t.Error("an unknown provider was accepted by the CHECK constraint")
	}

	// Rotation replaces the ciphertext; delete removes it for good and returns the org to the default.
	before := append([]byte(nil), rec[0].Sealed...)
	if _, err := svc.Set(ctxA, "openai", application.LLMInput{APIKey: pgTestKey + "-v2", Model: "gpt-4.1"}); err != nil {
		t.Fatal(err)
	}
	after, _ := st.ListLLMCredentials(ctx, "org-a")
	if bytes.Equal(before, after[0].Sealed) || after[0].CreatedAt.After(after[0].UpdatedAt) {
		t.Error("rotation did not change the ciphertext")
	}
	if _, err := svc.Delete(ctxA, "openai"); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListLLMCredentials(ctx, "org-a"); len(l) != 0 {
		t.Fatal("delete kept the credential")
	}
	if a, _ := st.GetLLMActive(ctx, "org-a"); a != "" {
		t.Fatalf("delete of the active credential left active = %q", a)
	}
	if err := st.DeleteLLMCredential(ctx, "org-a", "openai"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete twice = %v", err)
	}
}

func TestMigration580DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.PutLLMCredential(ctx, "org-a", application.LLMCredentialRecord{Provider: "kimi", Model: "kimi-latest", Sealed: []byte("x"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/580_llm_credentials_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var a, b *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('org_llm_credentials')::text`).Scan(&a)
	_ = owner.QueryRow(ctx, `SELECT to_regclass('org_llm_settings')::text`).Scan(&b)
	if a != nil || b != nil {
		t.Fatal("down left the tables")
	}
	up, err := migrationsFS.ReadFile("migrations/580_llm_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
}
