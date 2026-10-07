package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// storeContract is run against every Store implementation (memory always,
// Postgres when TEST_DATABASE_URL is set; see pg_integration_test.go).
func storeContract(t *testing.T, st Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	mkAccount := func(email, slug string) (User, Org, Membership) {
		u := User{ID: uuid.NewString(), Email: email, Name: "N", PasswordHash: "hash", CreatedAt: now}
		o := Org{ID: uuid.NewString(), Name: "Org", Slug: slug}
		return u, o, Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: now}
	}
	slug := "s-" + randomHex(4)
	u1, o1, m1 := mkAccount("c1-"+randomHex(3)+"@example.com", slug)
	if err := st.CreateAccount(bg, u1, o1, m1); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// Conflicts.
	u2, o2, m2 := mkAccount(u1.Email, "s-"+randomHex(4))
	if err := st.CreateAccount(bg, u2, o2, m2); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: %v", err)
	}
	u3, o3, m3 := mkAccount("c3-"+randomHex(3)+"@example.com", slug)
	if err := st.CreateAccount(bg, u3, o3, m3); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug: %v", err)
	}
	// Failed creations leave nothing behind.
	if _, err := st.UserByID(bg, u3.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial account persisted: %v", err)
	}
	if _, err := st.GetMembership(bg, o3.ID, u3.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial membership persisted: %v", err)
	}

	got, err := st.UserByEmail(bg, u1.Email)
	if err != nil || got.ID != u1.ID || got.PasswordHash != "hash" || got.FailedAttempts != 0 || !got.LockedUntil.IsZero() {
		t.Fatalf("UserByEmail: %+v %v", got, err)
	}
	if _, err := st.UserByEmail(bg, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	// Memberships / roles / last owner.
	ua, oa, ma := mkAccount("c4-"+randomHex(3)+"@example.com", "s-"+randomHex(4))
	if err := st.CreateAccount(bg, ua, oa, ma); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership(bg, Membership{OrgID: o1.ID, UserID: ua.ID, Role: RoleViewer, CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if err := st.AddMembership(bg, Membership{OrgID: o1.ID, UserID: ua.ID, Role: RoleAdmin, CreatedAt: now}); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("dup membership: %v", err)
	}
	if err := st.AddMembership(bg, Membership{OrgID: o1.ID, UserID: uuid.NewString(), Role: RoleViewer, CreatedAt: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("membership for unknown user: %v", err)
	}
	ms, _ := st.MembershipsOf(bg, ua.ID)
	if len(ms) != 2 || ms[0].OrgID != oa.ID || ms[1].OrgID != o1.ID {
		t.Fatalf("MembershipsOf ordering: %+v", ms)
	}
	members, _ := st.ListMembers(bg, o1.ID)
	if len(members) != 2 {
		t.Fatalf("ListMembers: %+v", members)
	}
	for _, m := range members {
		if m.Email == "" || m.Role == "" {
			t.Fatalf("incomplete member %+v", m)
		}
	}
	if err := st.SetMemberRole(bg, o1.ID, u1.ID, RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote last owner: %v", err)
	}
	if err := st.RemoveMember(bg, o1.ID, u1.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remove last owner: %v", err)
	}
	if err := st.SetMemberRole(bg, o1.ID, ua.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMemberRole(bg, o1.ID, u1.ID, RoleAdmin); err != nil {
		t.Fatalf("demote with 2 owners: %v", err)
	}
	if err := st.SetMemberRole(bg, o1.ID, uuid.NewString(), RoleAdmin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set role on unknown: %v", err)
	}
	// Roles in another org are untouched.
	if m, _ := st.GetMembership(bg, oa.ID, ua.ID); m.Role != RoleOwner {
		t.Fatal("other org changed")
	}
	if err := st.RemoveMember(bg, o1.ID, u1.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveMember(bg, o1.ID, u1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double remove: %v", err)
	}

	// Lockout counters.
	for i := 1; i <= 3; i++ {
		u, err := st.RecordLoginFailure(bg, ua.ID, 3, time.Minute, now)
		if err != nil || u.FailedAttempts != i {
			t.Fatalf("failure %d: %+v %v", i, u, err)
		}
		if locked := u.LockedUntil.After(now); locked != (i == 3) {
			t.Fatalf("failure %d: locked=%v", i, locked)
		}
	}
	u, _ := st.RecordLoginFailure(bg, ua.ID, 3, time.Minute, now.Add(2*time.Minute)) // lock expired
	if u.FailedAttempts != 1 || u.LockedUntil.After(now.Add(2*time.Minute)) {
		t.Fatalf("after expiry: %+v", u)
	}
	if err := st.ResetLoginFailures(bg, ua.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.UserByID(bg, ua.ID); u.FailedAttempts != 0 || !u.LockedUntil.IsZero() {
		t.Fatalf("reset: %+v", u)
	}
	if err := st.UpdatePasswordHash(bg, ua.ID, "newhash"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.UserByID(bg, ua.ID); u.PasswordHash != "newhash" {
		t.Fatal("hash not updated")
	}

	// Refresh tokens.
	tok := func(user, org, family string, ttl time.Duration) RefreshToken {
		return RefreshToken{ID: uuid.NewString(), UserID: user, OrgID: org, FamilyID: family,
			TokenHash: randomHex(16), ExpiresAt: now.Add(ttl), CreatedAt: now}
	}
	fam := uuid.NewString()
	t0 := tok(ua.ID, oa.ID, fam, time.Hour)
	if err := st.SaveRefreshToken(bg, t0); err != nil {
		t.Fatal(err)
	}
	next1 := RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	old, err := st.RotateRefreshToken(bg, t0.TokenHash, next1, now)
	if err != nil || old.UserID != ua.ID || old.OrgID != oa.ID || old.FamilyID != fam {
		t.Fatalf("rotate: %+v %v", old, err)
	}
	next2 := RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	got1, err := st.RotateRefreshToken(bg, next1.TokenHash, next2, now) // proves next1 inherited identity
	if err != nil || got1.FamilyID != fam || got1.UserID != ua.ID || got1.OrgID != oa.ID {
		t.Fatalf("rotate #2: %+v %v", got1, err)
	}
	// Replay of t0 -> reuse; whole family (including next2) dies.
	if _, err := st.RotateRefreshToken(bg, t0.TokenHash, RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour)}, now); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := st.RotateRefreshToken(bg, next2.TokenHash, RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour)}, now); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("family should be revoked: %v", err)
	}
	if _, err := st.RotateRefreshToken(bg, "unknown", RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16)}, now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown: %v", err)
	}
	exp := tok(ua.ID, oa.ID, uuid.NewString(), time.Minute)
	st.SaveRefreshToken(bg, exp)
	if _, err := st.RotateRefreshToken(bg, exp.TokenHash, RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour)}, now.Add(time.Hour)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}

	// Bulk revocation.
	a1, a2, b1 := tok(ua.ID, oa.ID, uuid.NewString(), time.Hour), tok(ua.ID, o1.ID, uuid.NewString(), time.Hour), tok(u1.ID, o1.ID, uuid.NewString(), time.Hour)
	for _, x := range []RefreshToken{a1, a2, b1} {
		st.SaveRefreshToken(bg, x)
	}
	revoked := func(x RefreshToken) bool {
		_, err := st.RotateRefreshToken(bg, x.TokenHash, RefreshToken{ID: uuid.NewString(), TokenHash: randomHex(16), ExpiresAt: now.Add(time.Hour)}, now)
		return errors.Is(err, ErrTokenReuse)
	}
	if err := st.RevokeOrgUserTokens(bg, o1.ID, ua.ID, now); err != nil {
		t.Fatal(err)
	}
	if !revoked(a2) {
		t.Fatal("a2 should be revoked")
	}
	if err := st.RevokeFamilyOf(bg, a1.TokenHash, now); err != nil {
		t.Fatal(err)
	}
	if !revoked(a1) {
		t.Fatal("a1 should be revoked")
	}
	if err := st.RevokeFamilyOf(bg, "nope", now); err != nil {
		t.Fatal("unknown family revoke must be a no-op")
	}
	if err := st.RevokeUserTokens(bg, u1.ID, now); err != nil {
		t.Fatal(err)
	}
	if !revoked(b1) {
		t.Fatal("b1 should be revoked")
	}
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }
