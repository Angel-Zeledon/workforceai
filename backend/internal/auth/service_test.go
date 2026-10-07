package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var bg = context.Background()

func TestRegisterCreatesOwnerAndSession(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "Owner@Example.com ", "Acme Corp")
	if s.Role != RoleOwner || s.OrgID == "" || s.User.Email != "owner@example.com" {
		t.Fatalf("session = %+v", s)
	}
	if s.TokenType != "Bearer" || s.AccessToken == "" || s.RefreshToken == "" || s.ExpiresIn != 900 {
		t.Fatalf("session tokens = %+v", s)
	}
	p := e.principal(t, s)
	if p.UserID != s.User.ID || p.OrgID != s.OrgID || p.Role != RoleOwner {
		t.Fatalf("principal = %+v", p)
	}
	u, _ := e.store.UserByID(bg, s.User.ID)
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") || strings.Contains(u.PasswordHash, goodPW) {
		t.Fatal("password must be stored as argon2id hash")
	}
}

func TestRegisterValidationAndDuplicates(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Register(bg, RegisterInput{Email: "bad", Password: "x", Name: "", OrgName: "A"}, ClientMeta{})
	var ve *ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, ErrValidation) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	for _, f := range []string{"email", "password", "name", "org_name"} {
		if ve.Fields[f] == "" {
			t.Errorf("missing field error %q: %v", f, ve.Fields)
		}
	}
	e.register(t, "dup@example.com", "One")
	_, err = e.svc.Register(bg, RegisterInput{Email: "DUP@example.com", Password: goodPW, Name: "N", OrgName: "Two"}, ClientMeta{})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
}

func TestLoginSuccessAndFailure(t *testing.T) {
	e := newEnv(t)
	reg := e.register(t, "a@example.com", "Org A")
	s, err := e.svc.Login(bg, LoginInput{Email: "A@example.com", Password: goodPW}, ClientMeta{})
	if err != nil || s.OrgID != reg.OrgID || s.Role != RoleOwner {
		t.Fatalf("login: %+v %v", s, err)
	}
	if _, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: "wrong-password-1"}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad password: %v", err)
	}
	// Unknown user is indistinguishable from bad password.
	if _, err := e.svc.Login(bg, LoginInput{Email: "ghost@example.com", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	for _, in := range []LoginInput{{}, {Email: "not-an-email", Password: "x"}, {Email: "a@example.com"}} {
		if _, err := e.svc.Login(bg, in, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("malformed login %+v: %v", in, err)
		}
	}
}

func TestLoginPicksRequestedOrgOnlyIfMember(t *testing.T) {
	e := newEnv(t)
	a := e.register(t, "a@example.com", "Org A")
	e.clock.Advance(time.Second)
	b := e.register(t, "b@example.com", "Org B")
	e.clock.Advance(time.Second)
	if _, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW, OrgID: b.OrgID}, ClientMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("login into foreign org must be forbidden, got %v", err)
	}
	// Once added as viewer to B, A can pick org B and gets the viewer role.
	if _, err := e.svc.AddMember(bg, e.principal(t, b), "a@example.com", RoleViewer); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW, OrgID: b.OrgID}, ClientMeta{})
	if err != nil || s.OrgID != b.OrgID || s.Role != RoleViewer {
		t.Fatalf("got %+v %v", s, err)
	}
	s, _ = e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW}, ClientMeta{})
	if s.OrgID != a.OrgID {
		t.Fatal("default org should be the oldest membership")
	}
}

func TestLockoutAfterFailedAttempts(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxFailedAttempts = 3; c.LockDuration = 10 * time.Minute })
	e.register(t, "lock@example.com", "L")
	bad := LoginInput{Email: "lock@example.com", Password: "wrong-password-1"}
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Login(bg, bad, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, err := e.svc.Login(bg, bad, ClientMeta{})
	var le *LockedError
	if !errors.Is(err, ErrAccountLocked) || !errors.As(err, &le) {
		t.Fatalf("3rd failure must lock, got %v", err)
	}
	if !le.Until.Equal(e.clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("lock until = %v", le.Until)
	}
	// While locked, even the CORRECT password is refused (and does not extend the lock).
	e.clock.Advance(5 * time.Minute)
	_, err = e.svc.Login(bg, LoginInput{Email: "lock@example.com", Password: goodPW}, ClientMeta{})
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("correct password while locked: %v", err)
	}
	// After the lock expires the correct password works and the counter resets.
	e.clock.Advance(6 * time.Minute)
	if _, err := e.svc.Login(bg, LoginInput{Email: "lock@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatalf("after lock expiry: %v", err)
	}
	u, _ := e.store.UserByEmail(bg, "lock@example.com")
	if u.FailedAttempts != 0 || !u.LockedUntil.IsZero() {
		t.Fatalf("counter not reset: %+v", u)
	}
}

func TestLockoutCounterResetsOnSuccess(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxFailedAttempts = 3 })
	e.register(t, "r@example.com", "R")
	bad := LoginInput{Email: "r@example.com", Password: "wrong-password-1"}
	for round := 0; round < 3; round++ {
		e.svc.Login(bg, bad, ClientMeta{})
		e.svc.Login(bg, bad, ClientMeta{})
		if _, err := e.svc.Login(bg, LoginInput{Email: "r@example.com", Password: goodPW}, ClientMeta{}); err != nil {
			t.Fatalf("round %d: success must not be locked out: %v", round, err)
		}
	}
}

func TestLockoutAfterExpiredLockStartsFresh(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxFailedAttempts = 2; c.LockDuration = time.Minute })
	e.register(t, "f@example.com", "F")
	bad := LoginInput{Email: "f@example.com", Password: "wrong-password-1"}
	e.svc.Login(bg, bad, ClientMeta{})
	e.svc.Login(bg, bad, ClientMeta{}) // locked
	e.clock.Advance(2 * time.Minute)
	// One failure after expiry must NOT immediately re-lock.
	if _, err := e.svc.Login(bg, bad, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("first failure after lock expiry: %v", err)
	}
}

func TestLoginEmailRateLimiter(t *testing.T) {
	lim := NewMemoryLimiter()
	e := newEnv(t, func(c *Config) {
		c.LoginLimiter = lim
		c.LoginLimit = Limit{Max: 3, Window: time.Minute}
		c.MaxFailedAttempts = 100
	})
	lim.SetClock(e.clock.Now)
	for i := 0; i < 3; i++ {
		e.svc.Login(bg, LoginInput{Email: "nobody@example.com", Password: goodPW}, ClientMeta{})
	}
	_, err := e.svc.Login(bg, LoginInput{Email: "nobody@example.com", Password: goodPW}, ClientMeta{})
	var rl *RateLimitedError
	if !errors.Is(err, ErrRateLimited) || !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("want rate limited for non-existent account too, got %v", err)
	}
	// Different email is unaffected.
	if _, err := e.svc.Login(bg, LoginInput{Email: "other@example.com", Password: goodPW}, ClientMeta{}); errors.Is(err, ErrRateLimited) {
		t.Fatal("limit must be per email")
	}
}

func TestRefreshRotation(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "a@example.com", "A")
	e.clock.Advance(time.Minute)
	n, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if n.RefreshToken == s.RefreshToken || n.AccessToken == s.AccessToken {
		t.Fatal("tokens must rotate")
	}
	if n.OrgID != s.OrgID || n.User.ID != s.User.ID || n.Role != RoleOwner {
		t.Fatalf("session changed identity: %+v", n)
	}
	if _, err := e.svc.Authenticate(bg, n.AccessToken); err != nil {
		t.Fatalf("new access token invalid: %v", err)
	}
}

func TestRefreshReuseRevokesFamily(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "a@example.com", "A")
	n1, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// Attacker replays the already-rotated token.
	if _, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{}); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("replay must be detected, got %v", err)
	}
	// The legitimate (newest) token of the family is now dead too.
	if _, err := e.svc.Refresh(bg, n1.RefreshToken, ClientMeta{}); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("family must be revoked after reuse, got %v", err)
	}
	if e.store.ActiveTokens(s.User.ID) != 0 {
		t.Fatal("no active tokens should remain")
	}
	// A fresh login (new family) still works.
	if _, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshExpiredAndUnknown(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.RefreshTTL = time.Hour })
	s := e.register(t, "a@example.com", "A")
	for _, tok := range []string{"", "unknown-token", strings.Repeat("a", 500), s.AccessToken} {
		if _, err := e.svc.Refresh(bg, tok, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %.20q: want ErrInvalidToken, got %v", tok, err)
		}
	}
	e.clock.Advance(2 * time.Hour)
	if _, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired refresh: %v", err)
	}
}

func TestRefreshConcurrentOnlyOneWins(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "a@example.com", "A")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d concurrent refreshes succeeded, want exactly 1", ok)
	}
}

func TestLogoutRevokesRefresh(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "a@example.com", "A")
	n, _ := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{})
	if err := e.svc.Logout(bg, n.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Refresh(bg, n.RefreshToken, ClientMeta{}); err == nil {
		t.Fatal("refresh after logout must fail")
	}
	// Idempotent / safe for garbage.
	if err := e.svc.Logout(bg, n.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Logout(bg, "garbage"); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutAll(t *testing.T) {
	e := newEnv(t)
	s1 := e.register(t, "a@example.com", "A")
	s2, _ := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW}, ClientMeta{})
	if err := e.svc.LogoutAll(bg, s1.User.ID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{s1.RefreshToken, s2.RefreshToken} {
		if _, err := e.svc.Refresh(bg, tok, ClientMeta{}); err == nil {
			t.Fatal("refresh must fail after logout-all")
		}
	}
}

func TestPasswordRehashOnLogin(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "a@example.com", "A")
	// Replace the hasher with a stronger configuration: next login upgrades the hash.
	e.svc.hasher = PasswordHasher{Params: PasswordParams{Memory: 16, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}}
	before, _ := e.store.UserByID(bg, s.User.ID)
	if _, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	after, _ := e.store.UserByID(bg, s.User.ID)
	if after.PasswordHash == before.PasswordHash || !strings.Contains(after.PasswordHash, "m=16,t=2") {
		t.Fatalf("hash not upgraded: %s", after.PasswordHash)
	}
	if _, err := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatal("login with upgraded hash failed")
	}
}

func TestAccessTokenExpiryViaService(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AccessTTL = time.Minute })
	s := e.register(t, "a@example.com", "A")
	e.clock.Advance(59 * time.Second)
	if _, err := e.svc.Authenticate(bg, s.AccessToken); err != nil {
		t.Fatalf("still valid: %v", err)
	}
	e.clock.Advance(2 * time.Second)
	if _, err := e.svc.Authenticate(bg, s.AccessToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestServiceConfigValidation(t *testing.T) {
	if _, err := NewService(Config{Secret: testSecret}); err == nil {
		t.Fatal("store required")
	}
	if _, err := NewService(Config{Store: NewMemoryStore(), Secret: []byte("short")}); err == nil {
		t.Fatal("short secret must fail")
	}
}

// ---------- multi-tenancy & privilege escalation ----------

func TestTwoOrgsAreIsolatedForMembers(t *testing.T) {
	e := newEnv(t)
	a := e.register(t, "owner-a@example.com", "Org A")
	b := e.register(t, "owner-b@example.com", "Org B")
	pa, pb := e.principal(t, a), e.principal(t, b)
	if pa.OrgID == pb.OrgID {
		t.Fatal("orgs must differ")
	}
	ma, _ := e.svc.ListMembers(bg, pa)
	mb, _ := e.svc.ListMembers(bg, pb)
	if len(ma) != 1 || ma[0].UserID != a.User.ID || len(mb) != 1 || mb[0].UserID != b.User.ID {
		t.Fatalf("members leak across orgs: %+v / %+v", ma, mb)
	}
	// Org A's owner cannot see, change, or remove a user of org B.
	if err := e.svc.ChangeMemberRole(bg, pa, b.User.ID, RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org role change: %v", err)
	}
	if err := e.svc.RemoveMember(bg, pa, b.User.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org remove: %v", err)
	}
	// An actor principal whose OrgID is forged to another org is rejected.
	forged := Principal{UserID: a.User.ID, OrgID: pb.OrgID, Role: RoleOwner}
	if _, err := e.svc.ListMembers(bg, forged); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("forged principal org: %v", err)
	}
	if err := e.svc.ChangeMemberRole(bg, forged, b.User.ID, RoleViewer); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("forged principal org (change): %v", err)
	}
	// Still intact.
	if m, _ := e.store.GetMembership(bg, pb.OrgID, b.User.ID); m.Role != RoleOwner {
		t.Fatal("org B owner was modified")
	}
}

func TestTokenForOrgWithoutMembershipRejected(t *testing.T) {
	e := newEnv(t)
	a := e.register(t, "a@example.com", "A")
	b := e.register(t, "b@example.com", "B")
	// Validly signed token (attacker with the secret, or a logic bug) naming
	// user A inside org B must still be rejected: membership is checked in the DB.
	tok, _ := e.svc.signer.Sign(Claims{Subject: a.User.ID, OrgID: b.OrgID, Role: RoleOwner,
		IssuedAt: e.clock.Now().Unix(), ExpiresAt: e.clock.Now().Add(time.Hour).Unix()})
	if _, err := e.svc.Authenticate(bg, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v", err)
	}
}

func TestRoleComesFromDBNotFromToken(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "o@example.com", "O")
	uid := e.addUserTo(t, owner, "m@example.com", RoleMember)
	// Token claiming "owner" for a user that is only a member in the DB.
	tok, _ := e.svc.signer.Sign(Claims{Subject: uid, OrgID: owner.OrgID, Role: RoleOwner,
		IssuedAt: e.clock.Now().Unix(), ExpiresAt: e.clock.Now().Add(time.Hour).Unix()})
	p, err := e.svc.Authenticate(bg, tok)
	if err != nil || p.Role != RoleMember {
		t.Fatalf("principal role = %v err=%v, want member", p.Role, err)
	}
}

func TestDemotionTakesEffectImmediately(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "o@example.com", "O")
	uid := e.addUserTo(t, owner, "a@example.com", RoleAdmin)
	s, _ := e.svc.Login(bg, LoginInput{Email: "a@example.com", Password: goodPW, OrgID: owner.OrgID}, ClientMeta{})
	if p := e.principal(t, s); p.Role != RoleAdmin {
		t.Fatalf("role = %s", p.Role)
	}
	if err := e.svc.ChangeMemberRole(bg, e.principal(t, owner), uid, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if p := e.principal(t, s); p.Role != RoleViewer { // same access token
		t.Fatalf("stale role %s after demotion", p.Role)
	}
	// Removal kills the access token and the refresh token for that org.
	if err := e.svc.RemoveMember(bg, e.principal(t, owner), uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(bg, s.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("removed member still authenticated: %v", err)
	}
	if _, err := e.svc.Refresh(bg, s.RefreshToken, ClientMeta{}); err == nil {
		t.Fatal("removed member can still refresh")
	}
}

func TestTrustTokenClaimsMode(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.TrustTokenClaims = true })
	s := e.register(t, "a@example.com", "A")
	p := e.principal(t, s)
	if p.Role != RoleOwner {
		t.Fatal(p.Role)
	}
	bad, _ := e.svc.signer.Sign(Claims{Subject: "u", OrgID: "o", Role: "godmode",
		IssuedAt: e.clock.Now().Unix(), ExpiresAt: e.clock.Now().Add(time.Hour).Unix()})
	if _, err := e.svc.Authenticate(bg, bad); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown role must be rejected, got %v", err)
	}
}

func TestPrivilegeEscalationBlocked(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "owner@example.com", "O")
	adminID := e.addUserTo(t, owner, "admin@example.com", RoleAdmin)
	memberID := e.addUserTo(t, owner, "member@example.com", RoleMember)
	viewerID := e.addUserTo(t, owner, "viewer@example.com", RoleViewer)
	otherAdminID := e.addUserTo(t, owner, "admin2@example.com", RoleAdmin)

	login := func(email string) Principal {
		s, err := e.svc.Login(bg, LoginInput{Email: email, Password: goodPW, OrgID: owner.OrgID}, ClientMeta{})
		if err != nil {
			t.Fatal(err)
		}
		return e.principal(t, s)
	}
	admin, member, viewer := login("admin@example.com"), login("member@example.com"), login("viewer@example.com")
	ownerP := e.principal(t, owner)

	type tc struct {
		name string
		err  error
		want error
	}
	cases := []tc{
		// members and viewers cannot manage anyone
		{"member promotes self to owner", e.svc.ChangeMemberRole(bg, member, memberID, RoleOwner), ErrForbidden},
		{"member promotes self to admin", e.svc.ChangeMemberRole(bg, member, memberID, RoleAdmin), ErrForbidden},
		{"viewer promotes self to member", e.svc.ChangeMemberRole(bg, viewer, viewerID, RoleMember), ErrForbidden},
		{"member demotes admin", e.svc.ChangeMemberRole(bg, member, adminID, RoleViewer), ErrForbidden},
		{"member removes viewer", e.svc.RemoveMember(bg, member, viewerID), ErrForbidden},
		{"viewer removes member", e.svc.RemoveMember(bg, viewer, memberID), ErrForbidden},
		// admins can't climb
		{"admin promotes self to owner", e.svc.ChangeMemberRole(bg, admin, adminID, RoleOwner), ErrForbidden},
		{"admin re-roles self", e.svc.ChangeMemberRole(bg, admin, adminID, RoleMember), ErrForbidden},
		{"admin promotes member to owner", e.svc.ChangeMemberRole(bg, admin, memberID, RoleOwner), ErrForbidden},
		{"admin promotes member to admin", e.svc.ChangeMemberRole(bg, admin, memberID, RoleAdmin), ErrForbidden},
		{"admin demotes owner", e.svc.ChangeMemberRole(bg, admin, owner.User.ID, RoleViewer), ErrForbidden},
		{"admin demotes other admin", e.svc.ChangeMemberRole(bg, admin, otherAdminID, RoleViewer), ErrForbidden},
		{"admin removes owner", e.svc.RemoveMember(bg, admin, owner.User.ID), ErrForbidden},
		{"admin removes other admin", e.svc.RemoveMember(bg, admin, otherAdminID), ErrForbidden},
		// forged principal role is ignored (re-read from DB)
		{"member forges owner principal", e.svc.ChangeMemberRole(bg, Principal{UserID: memberID, OrgID: owner.OrgID, Role: RoleOwner}, memberID, RoleOwner), ErrForbidden},
		{"viewer forges admin principal (add)", func() error {
			_, err := e.svc.AddMember(bg, Principal{UserID: viewerID, OrgID: owner.OrgID, Role: RoleAdmin}, "admin@example.com", RoleViewer)
			return err
		}(), ErrForbidden},
		// invalid role values
		{"assign unknown role", e.svc.ChangeMemberRole(bg, ownerP, memberID, "superuser"), ErrValidation},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}

	// admin cannot invite as admin/owner, but can invite member/viewer
	e.register(t, "newbie@example.com", "Newbie org")
	if _, err := e.svc.AddMember(bg, admin, "newbie@example.com", RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin inviting admin: %v", err)
	}
	if _, err := e.svc.AddMember(bg, admin, "newbie@example.com", RoleOwner); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin inviting owner: %v", err)
	}
	if _, err := e.svc.AddMember(bg, member, "newbie@example.com", RoleViewer); !errors.Is(err, ErrForbidden) {
		t.Errorf("member inviting: %v", err)
	}
	if _, err := e.svc.AddMember(bg, admin, "newbie@example.com", RoleMember); err != nil {
		t.Errorf("admin inviting member should work: %v", err)
	}
	if _, err := e.svc.AddMember(bg, admin, "newbie@example.com", RoleMember); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("duplicate add: %v", err)
	}

	// None of the above changed anything.
	for id, want := range map[string]Role{adminID: RoleAdmin, memberID: RoleMember, viewerID: RoleViewer,
		owner.User.ID: RoleOwner, otherAdminID: RoleAdmin} {
		if m, _ := e.store.GetMembership(bg, owner.OrgID, id); m.Role != want {
			t.Errorf("role of %s changed to %s, want %s", id, m.Role, want)
		}
	}

	// Legit actions still work: admin demotes member to viewer; owner promotes viewer to admin.
	if err := e.svc.ChangeMemberRole(bg, admin, memberID, RoleViewer); err != nil {
		t.Errorf("admin demoting member: %v", err)
	}
	if err := e.svc.ChangeMemberRole(bg, ownerP, viewerID, RoleAdmin); err != nil {
		t.Errorf("owner promoting: %v", err)
	}
}

func TestLastOwnerProtected(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "owner@example.com", "O")
	p := e.principal(t, owner)
	if err := e.svc.ChangeMemberRole(bg, p, owner.User.ID, RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("self-demotion of last owner: %v", err)
	}
	if err := e.svc.RemoveMember(bg, p, owner.User.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("self-removal of last owner: %v", err)
	}
	// With a second owner, the first may step down.
	uid := e.addUserTo(t, owner, "second@example.com", RoleOwner)
	if err := e.svc.ChangeMemberRole(bg, p, owner.User.ID, RoleAdmin); err != nil {
		t.Fatalf("demote with another owner present: %v", err)
	}
	// The remaining owner is now the last one.
	s2, _ := e.svc.Login(bg, LoginInput{Email: "second@example.com", Password: goodPW, OrgID: owner.OrgID}, ClientMeta{})
	if err := e.svc.RemoveMember(bg, e.principal(t, s2), uid); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner removal: %v", err)
	}
}

func TestMemberCanLeave(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "o@example.com", "O")
	uid := e.addUserTo(t, owner, "v@example.com", RoleViewer)
	s, _ := e.svc.Login(bg, LoginInput{Email: "v@example.com", Password: goodPW, OrgID: owner.OrgID}, ClientMeta{})
	if err := e.svc.RemoveMember(bg, e.principal(t, s), uid); err != nil {
		t.Fatalf("viewer leaving: %v", err)
	}
	if _, err := e.store.GetMembership(bg, owner.OrgID, uid); !errors.Is(err, ErrNotFound) {
		t.Fatal("membership should be gone")
	}
}

func TestListMembersRequiresAuth(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.ListMembers(bg, Principal{}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v", err)
	}
}
