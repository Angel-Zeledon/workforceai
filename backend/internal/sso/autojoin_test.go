package sso

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/auth"
)

func TestAutoJoinIsOffByDefaultAndNeedsAVerifiedDomain(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	v, err := f.svc.GetAutoJoin(ctx, orgA)
	if err != nil || v.Enabled || v.Role != auth.RoleViewer || len(v.Domains) != 0 {
		t.Fatalf("default = %+v, %v", v, err)
	}
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleMember); !errors.Is(err, ErrAutoJoinNeedsDomain) {
		t.Fatalf("enable without a domain = %v", err)
	}
	f.verifyDomain(t, orgA, "acme.io")
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleOwner); err == nil {
		t.Fatal("owner can never be the auto-join role")
	}
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, "root"); err == nil {
		t.Fatal("unknown role must be refused")
	}
	v, err = f.svc.SetAutoJoin(ctx, orgA, "admin", true, "")
	if err != nil || !v.Enabled || v.Role != auth.RoleViewer || len(v.Domains) != 1 || v.Domains[0] != "acme.io" {
		t.Fatalf("enabled = %+v, %v", v, err)
	}
}

func TestAutoJoinForMatchesOnlyTheVerifiedDomainOfAnEnabledOrg(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.verifyDomain(t, orgA, "acme.io")
	// Verified but not enabled: no.
	if _, _, ok, _ := f.svc.AutoJoinFor(ctx, "ana@acme.io"); ok {
		t.Fatal("off by default")
	}
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	org, role, ok, err := f.svc.AutoJoinFor(ctx, " Ana@ACME.io ")
	if err != nil || !ok || org != orgA || role != auth.RoleMember {
		t.Fatalf("= %q %q %v %v", org, role, ok, err)
	}
	for _, e := range []string{"ana@other.io", "ana@sub.acme.io", "ana@acme.io.evil.com", "nonsense", "", "ana@gmail.com"} {
		if _, _, ok, _ := f.svc.AutoJoinFor(ctx, e); ok {
			t.Errorf("%q must not auto-join", e)
		}
	}
	// A domain that is only claimed (not verified) never joins anybody.
	if _, err := f.svc.AddDomain(ctx, orgA, "admin", "claimed.io"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := f.svc.AutoJoinFor(ctx, "x@claimed.io"); ok {
		t.Fatal("an unverified domain must not auto-join")
	}
}

func TestAutoJoinRespectsTheScimGate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, func(o *Options) {
		o.Gate = func(_ context.Context, _, email string) error {
			if email == "gone@acme.io" {
				return auth.ErrForbidden
			}
			return nil
		}
	})
	f.verifyDomain(t, orgA, "acme.io")
	_, _ = f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleViewer)
	if _, _, ok, _ := f.svc.AutoJoinFor(ctx, "gone@acme.io"); ok {
		t.Fatal("a deprovisioned account must not rejoin by domain")
	}
	if _, _, ok, _ := f.svc.AutoJoinFor(ctx, "here@acme.io"); !ok {
		t.Fatal("others still join")
	}
}

// At SSO: with JIT off, the auto-join setting lets the verified identity create its membership with the auto-join role;
// with JIT on, the SSO configuration's own role wins.
func TestSSOUsesAutoJoinWhenJITIsOff(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setup(t, func(in *ConfigInput) { in.JIT = false; in.DefaultRole = auth.RoleMember })

	login := func() auth.FederatedIdentity {
		code, err := f.svc.Complete(ctx, f.start(t, "ana@acme.io"), "good-code")
		if err != nil {
			t.Fatal(err)
		}
		id, err := f.svc.Exchange(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	if id := login(); id.JIT {
		t.Fatalf("no JIT and no auto-join: %+v", id)
	}
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if id := login(); !id.JIT || id.Role != auth.RoleViewer {
		t.Fatalf("auto-join at SSO: %+v", id)
	}
	// JIT configured explicitly: its role wins over the auto-join role.
	f.setup(t, func(in *ConfigInput) { in.JIT = true; in.DefaultRole = auth.RoleAdmin })
	if id := login(); !id.JIT || id.Role != auth.RoleAdmin {
		t.Fatalf("jit role must win: %+v", id)
	}
}
