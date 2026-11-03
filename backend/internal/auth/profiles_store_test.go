package auth

import (
	"errors"
	"testing"
	"time"
)

// profilesContract is run against every Store (memory always, Postgres with TEST_DATABASE_URL): organization setup,
// member profiles, sign-up intents, organizations created for an existing user and the team fields of invitations.
func profilesContract(t *testing.T, st Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u := User{ID: "u-" + randomHex(4), Email: "pc-" + randomHex(3) + "@example.com", Name: "Ana", PasswordHash: "h", CreatedAt: now}
	o := Org{ID: "o-" + randomHex(4), Name: "Despacho", Slug: "pc-" + randomHex(4)}
	if err := st.CreateAccount(bg, u, o, Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	// Organization setup.
	if _, err := st.GetOrgSetup(bg, o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("setup before put: %v", err)
	}
	if err := st.PutOrgSetup(bg, OrgSetup{OrgID: o.ID, Audience: AudienceTeam, Locale: "en", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s, err := st.GetOrgSetup(bg, o.ID)
	if err != nil || s.Audience != AudienceTeam || s.Locale != "en" || !s.SetupDoneAt.IsZero() {
		t.Fatalf("setup: %+v %v", s, err)
	}
	if err := st.MarkOrgSetupDone(bg, o.ID, now); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetOrgSetup(bg, o.ID); !s.SetupDoneAt.Equal(now) {
		t.Fatalf("setup done at = %v", s.SetupDoneAt)
	}
	if err := st.MarkOrgSetupDone(bg, "no-such-org", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark unknown org: %v", err)
	}

	// Member profiles (upsert by org + user).
	if _, err := st.GetMemberProfile(bg, o.ID, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profile before put: %v", err)
	}
	p := MemberProfile{OrgID: o.ID, UserID: u.ID, Profession: "legal", TeamName: "Despacho", Lead: true, Via: ViaSignup, CreatedAt: now, UpdatedAt: now}
	if err := st.PutMemberProfile(bg, p); err != nil {
		t.Fatal(err)
	}
	p.DepartmentID, p.TeamName, p.ProvisionedAt = "dept_1", "", now
	if err := st.PutMemberProfile(bg, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMemberProfile(bg, o.ID, u.ID)
	if err != nil || got.Profession != "legal" || got.DepartmentID != "dept_1" || got.TeamName != "" || !got.Lead || got.Via != ViaSignup || !got.ProvisionedAt.Equal(now) {
		t.Fatalf("profile: %+v %v", got, err)
	}
	if ps, err := st.ListMemberProfiles(bg, o.ID); err != nil || len(ps) != 1 || ps[0].UserID != u.ID {
		t.Fatalf("list profiles: %+v %v", ps, err)
	}
	if ps, _ := st.ListMemberProfiles(bg, "other-org"); len(ps) != 0 {
		t.Fatalf("profiles leak across organizations: %+v", ps)
	}

	// Sign-up intents: taken once.
	j := User{ID: "u-" + randomHex(4), Email: "pj-" + randomHex(3) + "@example.com", Name: "Luis", PasswordHash: "h", CreatedAt: now}
	if err := st.CreateUser(bg, j); err != nil {
		t.Fatal(err)
	}
	in := SignupIntent{UserID: j.ID, Audience: AudienceSolo, Profession: "sales", OrgName: "Luis SA", Locale: "es", CreatedAt: now}
	if err := st.PutSignupIntent(bg, in); err != nil {
		t.Fatal(err)
	}
	if got, err := st.TakeSignupIntent(bg, j.ID); err != nil || got.Profession != "sales" || got.OrgName != "Luis SA" {
		t.Fatalf("take: %+v %v", got, err)
	}
	if _, err := st.TakeSignupIntent(bg, j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second take: %v", err)
	}

	// An organization for an existing user.
	o2 := Org{ID: "o-" + randomHex(4), Name: "Luis SA", Slug: "pj-" + randomHex(4)}
	if err := st.CreateOrg(bg, o2, Membership{OrgID: o2.ID, UserID: j.ID, Role: RoleOwner, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if m, err := st.GetMembership(bg, o2.ID, j.ID); err != nil || m.Role != RoleOwner {
		t.Fatalf("membership of the new org: %+v %v", m, err)
	}
	if n, _ := st.OrgName(bg, o2.ID); n != "Luis SA" {
		t.Fatalf("org name = %q", n)
	}
	if err := st.CreateOrg(bg, Org{ID: "o-" + randomHex(4), Name: "Dup", Slug: o2.Slug}, Membership{OrgID: "x", UserID: j.ID, Role: RoleOwner}); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug: %v", err)
	}

	// Invitations keep their team, profession and lead mark.
	inv := Invitation{ID: "i-" + randomHex(4), OrgID: o.ID, Email: "pi-" + randomHex(3) + "@example.com", Role: RoleMember,
		TokenHash: randomHex(32), InvitedBy: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		DepartmentID: "dept_1", Profession: "legal", Lead: true}
	if err := st.CreateInvitation(bg, inv, now); err != nil {
		t.Fatal(err)
	}
	gi, err := st.InvitationByTokenHash(bg, inv.TokenHash)
	if err != nil || gi.DepartmentID != "dept_1" || gi.Profession != "legal" || !gi.Lead {
		t.Fatalf("invitation team fields: %+v %v", gi, err)
	}
	if l, _ := st.ListInvitations(bg, o.ID); len(l) != 1 || l[0].Profession != "legal" {
		t.Fatalf("listed invitation: %+v", l)
	}
}

func TestMemoryProfilesContract(t *testing.T) { profilesContract(t, NewMemoryStore()) }
