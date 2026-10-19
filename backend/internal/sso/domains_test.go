package sso

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{"Acme.COM": "acme.com", " acme.com. ": "acme.com", "mail.acme.co.uk": "mail.acme.co.uk", "ana@acme.io": "acme.io", "xn--bcher-kva.example.org": "xn--bcher-kva.example.org"}
	for in, want := range ok {
		if got, err := NormalizeDomain(in); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "localhost", "acme", "1.2.3.4", "::1", "-acme.com", "acme-.com", "ac me.com", "acme..com", "acme.c", "foo.local", "foo.internal",
		"co.uk", "com.br", "acme.123", "http://acme.com", "a_b.com"}
	for _, in := range bad {
		if _, err := NormalizeDomain(in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
	for _, free := range []string{"gmail.com", "Outlook.com", "hotmail.com", "icloud.com", "yahoo.com", "proton.me"} {
		if _, err := NormalizeDomain(free); !errors.Is(err, ErrFreeMail) {
			t.Errorf("%s = %v, want ErrFreeMail", free, err)
		}
	}
}

func TestDomainVerificationNeedsTheTokenInDNS(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	d, err := f.svc.AddDomain(ctx, orgA, "admin", "acme.io")
	if err != nil {
		t.Fatal(err)
	}
	if d.Verified || d.Challenge.Type != "TXT" || d.Challenge.Name != "_aiworkforce-challenge.acme.io" || len(d.Token) < 16 {
		t.Fatalf("challenge = %+v", d)
	}
	// Adding twice returns the same token (the administrator may have already published it).
	if d2, _ := f.svc.AddDomain(ctx, orgA, "admin", "ACME.io"); d2.Token != d.Token {
		t.Fatal("AddDomain must be idempotent")
	}
	// No record yet.
	if _, err := f.svc.VerifyDomain(ctx, orgA, "admin", "acme.io"); !errors.Is(err, ErrChallengeMissing) {
		t.Fatalf("no record = %v", err)
	}
	if f.svc.Available(ctx, "x@acme.io") {
		t.Fatal("an unverified domain must not route to the organization")
	}
	// A record with somebody else's token, or the right token under the wrong name, proves nothing.
	f.dns.txt[d.Challenge.Name] = []string{"aiworkforce-verification=not-my-token", "v=spf1 -all"}
	f.dns.txt["acme.io"] = []string{d.Challenge.Value}
	if _, err := f.svc.VerifyDomain(ctx, orgA, "admin", "acme.io"); !errors.Is(err, ErrChallengeMissing) {
		t.Fatalf("wrong record = %v", err)
	}
	// The right record (even quoted, even among others) verifies.
	f.dns.txt[d.Challenge.Name] = []string{"v=spf1 -all", `"` + d.Challenge.Value + `"`}
	v, err := f.svc.VerifyDomain(ctx, orgA, "admin", "acme.io")
	if err != nil || !v.Verified {
		t.Fatalf("verify = %+v %v", v, err)
	}
	if o, err := f.store.OrgForVerifiedDomain(ctx, "acme.io"); err != nil || o != orgA {
		t.Fatalf("index = %q %v", o, err)
	}
}

func TestADomainBelongsToOneOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.verifyDomain(t, "org-a", "acme.io")
	// A second organization can register the domain and even publish ITS token, but loses the race.
	d, err := f.svc.AddDomain(ctx, "org-b", "other", "acme.io")
	if err != nil {
		t.Fatal(err)
	}
	f.dns.txt[d.Challenge.Name] = []string{d.Challenge.Value}
	if _, err := f.svc.VerifyDomain(ctx, "org-b", "other", "acme.io"); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("second claim = %v, want ErrDomainTaken", err)
	}
	if o, _ := f.store.OrgForVerifiedDomain(ctx, "acme.io"); o != "org-a" {
		t.Fatalf("domain moved to %s", o)
	}
	// Once the owner removes it, the other organization can verify.
	if err := f.svc.RemoveDomain(ctx, "org-a", "admin", "acme.io"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VerifyDomain(ctx, "org-b", "other", "acme.io"); err != nil {
		t.Fatalf("after release = %v", err)
	}
}

func TestDomainsAreScopedToTheirOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.verifyDomain(t, "org-a", "acme.io")
	if ds, _ := f.svc.ListDomains(ctx, "org-b"); len(ds) != 0 {
		t.Fatalf("org-b sees %v", ds)
	}
	if err := f.svc.RemoveDomain(ctx, "org-b", "x", "acme.io"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org-b removing org-a's domain = %v", err)
	}
	if _, err := f.svc.VerifyDomain(ctx, "org-b", "x", "acme.io"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org-b verifying a domain it never added = %v", err)
	}
}

func TestFreeMailCannotBeAdded(t *testing.T) {
	if _, err := newFixture(t).svc.AddDomain(context.Background(), orgA, "a", "gmail.com"); !errors.Is(err, ErrFreeMail) {
		t.Fatalf("gmail.com = %v", err)
	}
}
