package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func kek(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func newVault(t *testing.T) (*Vault, *MemRepo) {
	t.Helper()
	kw, err := NewEnvKeyWrapper(kek(1))
	if err != nil {
		t.Fatal(err)
	}
	r := NewMemRepo()
	return New(r, kw), r
}

const canary = "CANARY_SECRET_9f2c_abcdefghijklmnop"

func TestPutUseRoundTripAndNoPlaintextAtRest(t *testing.T) {
	v, repo := newVault(t)
	ctx := context.Background()
	m, err := v.Put(ctx, "o1", "c1", KindAPIKey, SecretFromString(canary), "u1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || m.Hint != canary[len(canary)-4:] {
		t.Fatalf("meta %+v", m)
	}
	for _, c := range repo.RawCiphertexts() {
		if bytes.Contains(c, []byte(canary)) {
			t.Fatal("plaintext at rest")
		}
	}
	var got string
	if err := v.Use(ctx, "o1", "c1", func(s Secret) error { got = string(s.Reveal()); return nil }); err != nil {
		t.Fatal(err)
	}
	if got != canary {
		t.Fatal("round trip failed")
	}
}

func TestShortSecretHasNoHint(t *testing.T) {
	v, _ := newVault(t)
	m, _ := v.Put(context.Background(), "o1", "c1", KindAPIKey, SecretFromString("short"), "u", nil)
	if m.Hint != "" {
		t.Fatalf("hint leaked for short secret: %q", m.Hint)
	}
}

func TestSecretNeverFormats(t *testing.T) {
	s := SecretFromString(canary)
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("x", "s", s)
	for _, out := range []string{
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s), buf.String(),
		fmt.Sprint(struct{ S Secret }{s}),
	} {
		if strings.Contains(out, canary) {
			t.Fatalf("secret printed: %s", out)
		}
	}
	b, _ := jsonMarshal(s)
	if strings.Contains(string(b), canary) {
		t.Fatal("secret marshalled")
	}
}

func TestFailClosedWithoutKEK(t *testing.T) {
	v := New(NewMemRepo(), nil)
	if _, err := v.Put(context.Background(), "o", "c", KindAPIKey, SecretFromString("x"), "u", nil); !errors.Is(err, ErrNoKEK) {
		t.Fatalf("want ErrNoKEK, got %v", err)
	}
	if err := v.Use(context.Background(), "o", "c", func(Secret) error { return nil }); !errors.Is(err, ErrNoKEK) {
		t.Fatalf("want ErrNoKEK, got %v", err)
	}
	if _, err := NewEnvKeyWrapper(""); !errors.Is(err, ErrNoKEK) {
		t.Fatal("empty KEK must be ErrNoKEK")
	}
	if _, err := NewEnvKeyWrapper("AAAA"); err == nil {
		t.Fatal("short KEK must be rejected")
	}
}

func TestAADBindsCiphertextToRow(t *testing.T) {
	v, repo := newVault(t)
	ctx := context.Background()
	_, _ = v.Put(ctx, "o1", "c1", KindAPIKey, SecretFromString("aaaaaaaaaaaaaaaaaaaaaaaa"), "u", nil)
	_, _ = v.Put(ctx, "o1", "c2", KindAPIKey, SecretFromString("bbbbbbbbbbbbbbbbbbbbbbbb"), "u", nil)
	repo.Swap(0, 1) // copy c1's ciphertext into c2's row and vice versa
	err := v.Use(ctx, "o1", "c2", func(Secret) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("swapped ciphertext must fail integrity check, got %v", err)
	}
}

func TestRotationKeepsOneActiveVersion(t *testing.T) {
	v, _ := newVault(t)
	ctx := context.Background()
	_, _ = v.Put(ctx, "o", "c", KindAPIKey, SecretFromString("first-first-first-first"), "u", nil)
	m, err := v.Put(ctx, "o", "c", KindAPIKey, SecretFromString("second-second-second-2"), "u", nil)
	if err != nil || m.Version != 2 {
		t.Fatalf("rotate: %v %+v", err, m)
	}
	var got string
	_ = v.Use(ctx, "o", "c", func(s Secret) error { got = string(s.Reveal()); return nil })
	if got != "second-second-second-2" {
		t.Fatalf("active is %q", got)
	}
}

func TestDestroyIsCryptoShred(t *testing.T) {
	v, repo := newVault(t)
	ctx := context.Background()
	_, _ = v.Put(ctx, "o", "c", KindAPIKey, SecretFromString(canary), "u", nil)
	if err := v.Destroy(ctx, "o", "c"); err != nil {
		t.Fatal(err)
	}
	if err := v.Use(ctx, "o", "c", func(Secret) error { return nil }); !errors.Is(err, ErrDestroyed) {
		t.Fatalf("want ErrDestroyed, got %v", err)
	}
	for _, c := range repo.RawCiphertexts() {
		if len(c) != 0 {
			t.Fatal("ciphertext survived destroy")
		}
	}
}

func TestDEKAndKEKRotation(t *testing.T) {
	ctx := context.Background()
	kw1, _ := NewEnvKeyWrapper(kek(1))
	repo := NewMemRepo()
	v := New(repo, kw1)
	_, _ = v.Put(ctx, "o", "old", KindAPIKey, SecretFromString("old-old-old-old-old-old"), "u", nil)
	if n, err := v.RotateDEK(ctx, "o"); err != nil || n != 2 {
		t.Fatalf("rotate dek: %d %v", n, err)
	}
	_, _ = v.Put(ctx, "o", "new", KindAPIKey, SecretFromString("new-new-new-new-new-new"), "u", nil)
	// KEK rotation: new KEK first, old kept for unwrapping.
	kw2, _ := NewEnvKeyWrapper(kek(2), kek(1))
	v2 := New(repo, kw2)
	if err := v2.RewrapKeys(ctx, "o"); err != nil {
		t.Fatal(err)
	}
	// After rewrap only the NEW kek is needed.
	kwOnly2, _ := NewEnvKeyWrapper(kek(2))
	v3 := New(repo, kwOnly2)
	for _, id := range []string{"old", "new"} {
		if err := v3.Use(ctx, "o", id, func(Secret) error { return nil }); err != nil {
			t.Fatalf("%s after KEK rotation: %v", id, err)
		}
	}
}

type reg struct{ got []string }

func (r *reg) Add(s string) { r.got = append(r.got, s) }

func TestSuspectsLearnSecrets(t *testing.T) {
	v, _ := newVault(t)
	r := &reg{}
	v.Suspects = r
	_, _ = v.Put(context.Background(), "o", "c", KindAPIKey, SecretFromString(canary), "u", nil)
	if len(r.got) != 1 || r.got[0] != canary {
		t.Fatalf("registrar not fed: %v", r.got)
	}
}

// Only the vault (and its Postgres repository, vault_repo.go) may reference the
// credentials table.
func TestOnlyVaultTouchesCredentialsTable(t *testing.T) {
	re := regexp.MustCompile(`(?i)(FROM|INTO|UPDATE|JOIN)\s+credentials\b`)
	root := filepath.Join("..")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "vault/") || rel == "infrastructure/postgres/vault_repo.go" {
			return nil
		}
		b, _ := os.ReadFile(p)
		if re.Match(b) {
			t.Errorf("%s references the credentials table", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
