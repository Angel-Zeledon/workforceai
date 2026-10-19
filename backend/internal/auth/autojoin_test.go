package auth

import (
	"context"
	"errors"
	"testing"
)

// fakeJoiner stands in for internal/sso: it knows one organization that owns acme.io and allows auto-join.
type fakeJoiner struct {
	org   string
	role  Role
	on    bool
	err   error
	calls int
}

func (f *fakeJoiner) AutoJoinFor(_ context.Context, email string) (string, Role, bool, error) {
	f.calls++
	if f.err != nil {
		return "", "", false, f.err
	}
	if f.on && len(email) > 8 && email[len(email)-8:] == "@acme.io" {
		return f.org, f.role, true, nil
	}
	return "", "", false, nil
}

func autoJoinEnv(t *testing.T, fj *fakeJoiner, mm *captureMailer) *env {
	return newEnv(t, func(c *Config) {
		c.RequireVerification = true
		c.Mailer = mm
		c.AppURL = "https://app.example.com/"
		c.AutoJoin = fj
	})
}

func memberRole(e *env, org, userEmail string) (Role, bool) {
	u, err := e.store.UserByEmail(context.Background(), userEmail)
	if err != nil {
		return "", false
	}
	m, err := e.store.GetMembership(context.Background(), org, u.ID)
	if err != nil {
		return "", false
	}
	return m.Role, true
}

func TestPasswordSignupAutoJoinsAfterTheAddressIsVerified(t *testing.T) {
	ctx := context.Background()
	mm := &captureMailer{}
	fj := &fakeJoiner{role: RoleViewer, on: true}
	e := autoJoinEnv(t, fj, mm)
	// The organization has to exist before people join it: register it without verification pending, by hand.
	st := e.store
	fj.org = "org-acme"
	_ = st.CreateAccount(ctx, User{ID: "u-boss", Email: "boss@acme.io", Name: "Boss", PasswordHash: "x", CreatedAt: e.clock.Now(), VerifiedAt: e.clock.Now()},
		Org{ID: "org-acme", Name: "Acme", Slug: "acme-x"}, Membership{OrgID: "org-acme", UserID: "u-boss", Role: RoleOwner, CreatedAt: e.clock.Now()})

	if err := register(e, "ana@acme.io", "en"); err == nil {
		t.Fatal("registration must wait for the verification link")
	}
	// Registering alone, with the address unconfirmed, joins nobody.
	if _, ok := memberRole(e, "org-acme", "ana@acme.io"); ok {
		t.Fatal("an unverified address must not join the organization")
	}
	if fj.calls != 0 {
		t.Fatalf("the auto-join lookup ran %d times before verification", fj.calls)
	}
	sess, err := e.svc.Verify(ctx, tokenIn(t, mm.last(t)), ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	role, ok := memberRole(e, "org-acme", "ana@acme.io")
	if !ok || role != RoleViewer {
		t.Fatalf("after verification: member=%v role=%q, want viewer", ok, role)
	}
	// The session lands in her own organization; the joined one is available to switch to.
	if sess.OrgID == "org-acme" {
		t.Fatal("the first session should be the user's own organization")
	}
	orgs, _ := e.svc.Orgs(ctx, sess.User.ID)
	found := false
	for _, o := range orgs {
		found = found || o.OrgID == "org-acme"
	}
	if !found {
		t.Fatalf("orgs = %+v", orgs)
	}
}

func TestPasswordAutoJoinIsOffByDefaultAndOnlyForTheDomain(t *testing.T) {
	ctx := context.Background()
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: false}
	e := autoJoinEnv(t, fj, mm)
	_ = e.store.CreateAccount(ctx, User{ID: "u-boss", Email: "boss@acme.io", Name: "B", PasswordHash: "x", CreatedAt: e.clock.Now(), VerifiedAt: e.clock.Now()},
		Org{ID: "org-acme", Name: "Acme", Slug: "acme-x"}, Membership{OrgID: "org-acme", UserID: "u-boss", Role: RoleOwner, CreatedAt: e.clock.Now()})

	_ = register(e, "ana@acme.io", "en")
	if _, err := e.svc.Verify(ctx, tokenIn(t, mm.last(t)), ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := memberRole(e, "org-acme", "ana@acme.io"); ok {
		t.Fatal("the setting is off: nobody joins")
	}

	fj.on = true
	_ = register(e, "luis@elsewhere.com", "en")
	if _, err := e.svc.Verify(ctx, tokenIn(t, mm.last(t)), ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := memberRole(e, "org-acme", "luis@elsewhere.com"); ok {
		t.Fatal("another domain must not join")
	}
}

func TestAutoJoinNeverGrantsOwnerAndSurvivesLookupFailure(t *testing.T) {
	ctx := context.Background()
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleOwner, on: true}
	e := autoJoinEnv(t, fj, mm)
	_ = e.store.CreateAccount(ctx, User{ID: "u-boss", Email: "boss@acme.io", Name: "B", PasswordHash: "x", CreatedAt: e.clock.Now(), VerifiedAt: e.clock.Now()},
		Org{ID: "org-acme", Name: "Acme", Slug: "acme-x"}, Membership{OrgID: "org-acme", UserID: "u-boss", Role: RoleOwner, CreatedAt: e.clock.Now()})

	_ = register(e, "ana@acme.io", "en")
	if _, err := e.svc.Verify(ctx, tokenIn(t, mm.last(t)), ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if role, ok := memberRole(e, "org-acme", "ana@acme.io"); !ok || role != RoleViewer {
		t.Fatalf("owner must be downgraded to viewer, got %q (member=%v)", role, ok)
	}

	// A failing lookup must not block the sign-in nor join anybody.
	fj.err = errors.New("db down")
	_ = register(e, "bea@acme.io", "en")
	sess, err := e.svc.Verify(ctx, tokenIn(t, mm.last(t)), ClientMeta{})
	if err != nil || sess == nil {
		t.Fatalf("verify must succeed even when auto-join fails: %v", err)
	}
	if _, ok := memberRole(e, "org-acme", "bea@acme.io"); ok {
		t.Fatal("a failed lookup must join nobody")
	}
}
