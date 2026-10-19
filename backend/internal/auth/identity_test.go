package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakePolicy struct {
	mu  sync.Mutex
	by  map[string]OrgPolicy
	err error
}

func (f *fakePolicy) set(org string, p OrgPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string]OrgPolicy{}
	}
	f.by[org] = p
}

func (f *fakePolicy) OrgPolicy(_ context.Context, org string) (OrgPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.by[org], f.err
}

func policyEnv(t *testing.T) (*env, *fakePolicy) {
	t.Helper()
	fp := &fakePolicy{}
	return newEnv(t, func(c *Config) { c.Policy = fp }), fp
}

func TestRequireMFAStopsLoginUntilEnrolled(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	s := e.register(t, "ana@acme.test", "Acme")
	fp.set(s.OrgID, OrgPolicy{RequireMFA: true})

	// A password login without a second factor gets no session, only an enrolment challenge.
	sess, err := e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{})
	var need *MFAEnrollmentRequiredError
	if sess != nil || !errors.As(err, &need) || need.Token == "" {
		t.Fatalf("login = %v, %v; want an enrolment challenge and no session", sess, err)
	}

	// The enrolment challenge is not a second-factor challenge, and vice versa.
	if _, err := e.svc.VerifyMFA(ctx, need.Token, "123456", ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("enrol token used as mfa token = %v, want ErrInvalidToken", err)
	}
	if _, err := e.svc.MFAEnrollSetup(ctx, e.svc.newChallenge(s.User.ID, s.OrgID, e.clock.Now())); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("mfa token used for enrolment = %v, want ErrInvalidToken", err)
	}
	if _, err := e.svc.MFAEnrollSetup(ctx, "garbage"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("garbage token = %v", err)
	}

	info, err := e.svc.MFAEnrollSetup(ctx, need.Token)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := b32.DecodeString(info.Secret)
	if _, _, err := e.svc.MFAEnrollComplete(ctx, need.Token, "000000", ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("wrong first code = %v", err)
	}
	sess, codes, err := e.svc.MFAEnrollComplete(ctx, need.Token, totpAt(secret, stepOf(e.clock.Now())), ClientMeta{})
	if err != nil || sess == nil || sess.AccessToken == "" || len(codes) != recoveryCodes {
		t.Fatalf("complete = %v %v %v", sess, codes, err)
	}
	// The token cannot enrol a second time.
	if _, _, err := e.svc.MFAEnrollComplete(ctx, need.Token, totpAt(secret, stepOf(e.clock.Now())+1), ClientMeta{}); !errors.Is(err, ErrMFAAlreadyEnabled) {
		t.Fatalf("second enrolment = %v, want ErrMFAAlreadyEnabled", err)
	}
	// From now on the normal second-factor challenge applies.
	_, err = e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	if !errors.As(err, &mr) {
		t.Fatalf("login after enrolment = %v, want MFARequiredError", err)
	}
}

func TestEnrolmentChallengeExpires(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	s := e.register(t, "ana@acme.test", "Acme")
	fp.set(s.OrgID, OrgPolicy{RequireMFA: true})
	_, err := e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{})
	var need *MFAEnrollmentRequiredError
	if !errors.As(err, &need) {
		t.Fatal(err)
	}
	e.clock.Advance(mfaChallengeTTL + time.Second)
	if _, err := e.svc.MFAEnrollSetup(ctx, need.Token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired enrolment token = %v, want ErrTokenExpired", err)
	}
}

func TestEnforceSSORefusesPasswordsButNotTheOwner(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	owner := e.register(t, "boss@acme.test", "Acme")
	fp.set(owner.OrgID, OrgPolicy{EnforceSSO: true})

	// A member created by the IdP has a random password, but even a known password must not open a session.
	hash, _ := e.svc.hasher.Hash(goodPW)
	u := User{ID: "u-member", Email: "emp@acme.test", Name: "Emp", PasswordHash: hash, CreatedAt: e.clock.Now(), VerifiedAt: e.clock.Now()}
	if err := e.store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddMembership(ctx, Membership{OrgID: owner.OrgID, UserID: u.ID, Role: RoleMember, CreatedAt: e.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "emp@acme.test", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("member password login = %v, want ErrSSORequired", err)
	}
	// Break-glass: the owner can still use the password, so a broken IdP never locks the organization out.
	if _, err := e.svc.Login(ctx, LoginInput{Email: "boss@acme.test", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatalf("owner login = %v", err)
	}
}

func TestUnreadablePolicyFailsClosed(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	e.register(t, "ana@acme.test", "Acme")
	fp.err = errors.New("db down")
	if s, err := e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{}); err == nil || s != nil {
		t.Fatalf("login with an unreadable policy = %v, %v; want an error and no session", s, err)
	}
}

func TestSwitchOrgHonoursRequireMFA(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	a := e.register(t, "ana@acme.test", "Acme")
	b := e.register(t, "bob@beta.test", "Beta")
	// Ana is also a member of Beta, which requires a second factor.
	if err := e.store.AddMembership(ctx, Membership{OrgID: b.OrgID, UserID: a.User.ID, Role: RoleMember, CreatedAt: e.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	fp.set(b.OrgID, OrgPolicy{RequireMFA: true})
	_, err := e.svc.SwitchOrg(ctx, Principal{UserID: a.User.ID, OrgID: a.OrgID, Role: RoleOwner}, b.OrgID, ClientMeta{})
	var need *MFAEnrollmentRequiredError
	if !errors.As(err, &need) || need.Token != "" {
		t.Fatalf("switch = %v; want an enrolment requirement without a token", err)
	}
}

func TestMFACannotBeDisabledWhileAnOrganizationRequiresIt(t *testing.T) {
	ctx := context.Background()
	e, fp := policyEnv(t)
	secret, _, uid := enrol(t, e, "ana@acme.test")
	ms, _ := e.store.MembershipsOf(ctx, uid)
	fp.set(ms[0].OrgID, OrgPolicy{RequireMFA: true})
	e.clock.Advance(time.Minute)
	err := e.svc.MFADisable(ctx, uid, goodPW, totpAt(secret, stepOf(e.clock.Now())))
	if !errors.Is(err, ErrMFARequiredByOrg) {
		t.Fatalf("disable = %v, want ErrMFARequiredByOrg", err)
	}
	if !e.svc.MFAEnabled(ctx, uid) {
		t.Fatal("two-factor must still be on")
	}
}

// ---- TOTP clock drift and replay, through the real login path ----

func TestTOTPDriftWindowAndReplayThroughLogin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	secret, _, _ := enrol(t, e, "ana@acme.test")
	e.clock.Advance(5 * time.Minute) // past the step used to enrol
	login := func() string {
		_, err := e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{})
		var mr *MFARequiredError
		if !errors.As(err, &mr) {
			t.Fatalf("login = %v", err)
		}
		return mr.Token
	}
	cur := stepOf(e.clock.Now())

	// A phone whose clock runs one step behind (30 s) is accepted ...
	if _, err := e.svc.VerifyMFA(ctx, login(), totpAt(secret, cur-1), ClientMeta{}); err != nil {
		t.Fatalf("code from the previous step = %v, want accepted", err)
	}
	// ... but a code that has been used cannot be used again, neither the same step nor an older one (replay).
	if _, err := e.svc.VerifyMFA(ctx, login(), totpAt(secret, cur-1), ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("replayed code = %v, want ErrMFAInvalidCode", err)
	}
	if _, err := e.svc.VerifyMFA(ctx, login(), totpAt(secret, cur-1), ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) && !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("replay again = %v", err)
	}
}

func TestTOTPOutsideTheDriftWindowIsRejected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	secret, _, _ := enrol(t, e, "ana@acme.test")
	e.clock.Advance(5 * time.Minute)
	_, err := e.svc.Login(ctx, LoginInput{Email: "ana@acme.test", Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	if !errors.As(err, &mr) {
		t.Fatal(err)
	}
	cur := stepOf(e.clock.Now())
	for _, d := range []int64{-2, -3, 2, 3} { // 60 s or more of drift: refused
		if _, err := e.svc.VerifyMFA(ctx, mr.Token, totpAt(secret, cur+d), ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
			t.Fatalf("drift %d steps = %v, want ErrMFAInvalidCode", d, err)
		}
	}
	// One step ahead (a fast phone clock) is still inside the window.
	if _, err := e.svc.VerifyMFA(ctx, mr.Token, totpAt(secret, cur+1), ClientMeta{}); err != nil {
		t.Fatalf("one step ahead = %v", err)
	}
}

// ---- federated sign-in ----

func TestSignInFederatedJIT(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	o := e.register(t, "boss@acme.test", "Acme")

	// Without JIT an unknown person cannot get in.
	if _, err := e.svc.SignInFederated(ctx, FederatedIdentity{OrgID: o.OrgID, Email: "new@acme.test", Name: "New"}, ClientMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("no JIT = %v, want ErrForbidden", err)
	}
	// With JIT the account and the membership are created; owner can never be granted.
	s, err := e.svc.SignInFederated(ctx, FederatedIdentity{OrgID: o.OrgID, Email: "New@Acme.test", Name: "New", Role: RoleOwner, JIT: true, Provider: "oidc"}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Role != RoleMember || s.OrgID != o.OrgID || s.User.Email != "new@acme.test" {
		t.Fatalf("session = %+v", s)
	}
	// Signing in again is the same account and the same role, and a stored role is not overwritten by a later default.
	s2, err := e.svc.SignInFederated(ctx, FederatedIdentity{OrgID: o.OrgID, Email: "new@acme.test", Role: RoleViewer, JIT: true}, ClientMeta{})
	if err != nil || s2.User.ID != s.User.ID || s2.Role != RoleMember {
		t.Fatalf("second sign-in = %+v %v", s2, err)
	}
	if _, err := e.svc.SignInFederated(ctx, FederatedIdentity{OrgID: o.OrgID, Email: "not-an-email", JIT: true}, ClientMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bad e-mail = %v", err)
	}
}

func TestSignInFederatedDoesNotEnterOtherOrganizations(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.register(t, "ana@acme.test", "Acme")
	b := e.register(t, "bob@beta.test", "Beta")
	// Ana exists in Acme only. A federated login for Beta without JIT must not add her to Beta.
	if _, err := e.svc.SignInFederated(ctx, FederatedIdentity{OrgID: b.OrgID, Email: "ana@acme.test"}, ClientMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-org = %v, want ErrForbidden", err)
	}
	if ms, _ := e.store.MembershipsOf(ctx, a.User.ID); len(ms) != 1 {
		t.Fatalf("memberships = %v", ms)
	}
}
