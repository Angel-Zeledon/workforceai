package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

// E1 "Alta y agentes por rol": who the account is for, the profession, invitations with a team, joining by domain.

func acmeOrg(t *testing.T, e *env) {
	t.Helper()
	now := e.clock.Now()
	if err := e.store.CreateAccount(context.Background(), User{ID: "u-boss", Email: "boss@acme.io", Name: "Boss", PasswordHash: "x", CreatedAt: now, VerifiedAt: now},
		Org{ID: "org-acme", Name: "Acme", Slug: "acme-x"}, Membership{OrgID: "org-acme", UserID: "u-boss", Role: RoleOwner, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterForATeamKeepsAudienceProfessionAndTeam(t *testing.T) {
	e := newEnv(t)
	s, err := e.svc.Register(bg, RegisterInput{Email: "ana@despacho.com", Password: goodPW, Name: "Ana Ruiz", OrgName: "Despacho Ruiz",
		Audience: AudienceTeam, Profession: "legal", TeamName: "Litigios", Locale: "es"}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.OrgSetup(bg, s.OrgID)
	if err != nil || st.Audience != AudienceTeam || st.Locale != "es" {
		t.Fatalf("org setup: %+v %v", st, err)
	}
	p, err := e.svc.MemberProfile(bg, s.OrgID, s.User.ID)
	if err != nil || p.Profession != "legal" || p.TeamName != "Litigios" || !p.Lead || p.Via != ViaSignup {
		t.Fatalf("profile: %+v %v", p, err)
	}
}

func TestRegisterSoloNeedsNoOrganizationName(t *testing.T) {
	e := newEnv(t)
	s, err := e.svc.Register(bg, RegisterInput{Email: "luis@example.com", Password: goodPW, Name: "Luis Mora", Audience: AudienceSolo, Profession: "sales"}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.OrgName(bg, s.OrgID); n != "Luis Mora" {
		t.Fatalf("a personal office is named after its person, got %q", n)
	}
	p, _ := e.svc.MemberProfile(bg, s.OrgID, s.User.ID)
	if p.Lead || p.TeamName != "" || p.Profession != "sales" {
		t.Fatalf("solo profile: %+v", p)
	}
}

func TestRegisterWithAudienceDefaultsTheProfessionToOther(t *testing.T) {
	e := newEnv(t)
	s, err := e.svc.Register(bg, RegisterInput{Email: "x@example.com", Password: goodPW, Name: "X Y", OrgName: "XY Co", Audience: AudienceCompany}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.MemberProfile(bg, s.OrgID, s.User.ID); p.Profession != "other" || !p.Lead {
		t.Fatalf("profile: %+v", p)
	}
}

func TestRegisterWithoutAudienceIsTheClassicSignUp(t *testing.T) {
	e := newEnv(t)
	s := e.register(t, "old@example.com", "Old")
	if _, err := e.svc.OrgSetup(bg, s.OrgID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a classic sign-up has no team setup: %v", err)
	}
	if _, err := e.svc.MemberProfile(bg, s.OrgID, s.User.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a classic sign-up has no profile: %v", err)
	}
}

func TestRegisterRejectsUnknownAudienceAndProfession(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Register(bg, RegisterInput{Email: "a@example.com", Password: goodPW, Name: "A B", OrgName: "AB", Audience: "everyone", Profession: "astronaut"}, ClientMeta{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["audience"] == "" || ve.Fields["profession"] == "" {
		t.Fatalf("want audience and profession errors, got %v", err)
	}
}

func TestSignupCheckOffersTheCompanyOnlyWhenItCanBeJoined(t *testing.T) {
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: true}
	e := autoJoinEnv(t, fj, mm)
	acmeOrg(t, e)
	if o, err := e.svc.SignupCheck(bg, "Ana@Acme.io"); err != nil || o.JoinOrgName != "Acme" {
		t.Fatalf("joinable: %+v %v", o, err)
	}
	if o, _ := e.svc.SignupCheck(bg, "ana@elsewhere.com"); o.JoinOrgName != "" {
		t.Fatalf("another domain: %+v", o)
	}
	fj.on = false
	if o, _ := e.svc.SignupCheck(bg, "ana@acme.io"); o.JoinOrgName != "" {
		t.Fatalf("auto-join off: %+v", o)
	}
	// Without e-mail verification the address can never be proven: nobody is offered to join.
	plain := newEnv(t, func(c *Config) { c.AutoJoin = &fakeJoiner{org: "org-acme", role: RoleMember, on: true} })
	acmeOrg(t, plain)
	if o, _ := plain.svc.SignupCheck(bg, "ana@acme.io"); o.JoinOrgName != "" {
		t.Fatalf("no verification: %+v", o)
	}
}

func TestJoinByDomainEntersTheCompanyInsteadOfCreatingAnother(t *testing.T) {
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: true}
	e := autoJoinEnv(t, fj, mm)
	acmeOrg(t, e)
	_, err := e.svc.Register(bg, RegisterInput{Email: "ana@acme.io", Password: goodPW, Name: "Ana", Join: true, Profession: "sales", Locale: "es"}, ClientMeta{})
	var vr *VerificationRequiredError
	if !errors.As(err, &vr) {
		t.Fatalf("join must wait for the verification link: %v", err)
	}
	u, _ := e.store.UserByEmail(bg, "ana@acme.io")
	if ms, _ := e.store.MembershipsOf(bg, u.ID); len(ms) != 0 {
		t.Fatalf("no organization before the address is proven: %+v", ms)
	}
	sess, err := e.svc.Verify(bg, tokenIn(t, mm.last(t)), ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if sess.OrgID != "org-acme" || sess.Role != RoleMember {
		t.Fatalf("session in %s as %s, want org-acme member", sess.OrgID, sess.Role)
	}
	if ms, _ := e.store.MembershipsOf(bg, u.ID); len(ms) != 1 {
		t.Fatalf("one organization only, got %+v", ms)
	}
	p, err := e.svc.MemberProfile(bg, "org-acme", u.ID)
	if err != nil || p.Profession != "sales" || p.Via != ViaDomain || p.DepartmentID != "" {
		t.Fatalf("profile: %+v %v", p, err)
	}
}

func TestJoinByDomainFallsBackToOwnOfficeWhenTheCompanyNoLongerLetsIn(t *testing.T) {
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: true}
	e := autoJoinEnv(t, fj, mm)
	acmeOrg(t, e)
	_, _ = e.svc.Register(bg, RegisterInput{Email: "bea@acme.io", Password: goodPW, Name: "Bea", Join: true, Profession: "hr", Locale: "en"}, ClientMeta{})
	fj.on = false // the company switched auto-join off before Bea opened her link
	sess, err := e.svc.Verify(bg, tokenIn(t, mm.last(t)), ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if sess.OrgID == "org-acme" || sess.Role != RoleOwner {
		t.Fatalf("want her own organization as owner, got %s %s", sess.OrgID, sess.Role)
	}
	if n, _ := e.store.OrgName(bg, sess.OrgID); n != "Bea" {
		t.Fatalf("own office name = %q", n)
	}
	st, _ := e.svc.OrgSetup(bg, sess.OrgID)
	p, _ := e.svc.MemberProfile(bg, sess.OrgID, sess.User.ID)
	if st.Audience != AudienceSolo || p.Profession != "hr" || p.Via != ViaSignup {
		t.Fatalf("setup %+v profile %+v", st, p)
	}
}

func TestJoinThatIsNotPossibleCreatesTheOrganizationAsUsual(t *testing.T) {
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: true}
	e := autoJoinEnv(t, fj, mm)
	acmeOrg(t, e)
	_, _ = e.svc.Register(bg, RegisterInput{Email: "eva@other.com", Password: goodPW, Name: "Eva", OrgName: "Eva SL", Join: true, Audience: AudienceSolo, Profession: "legal"}, ClientMeta{})
	u, _ := e.store.UserByEmail(bg, "eva@other.com")
	if ms, _ := e.store.MembershipsOf(bg, u.ID); len(ms) != 1 {
		t.Fatalf("own organization expected at once, got %+v", ms)
	}
}

type deptSet map[string]bool

func (d deptSet) exists(_ context.Context, org, id string) bool { return d[org+"/"+id] }

func TestInvitationWithTeamProfessionAndLead(t *testing.T) {
	depts := deptSet{}
	e := newEnv(t, func(c *Config) { c.DepartmentExists = depts.exists })
	owner := e.register(t, "boss@x.com", "X")
	depts[owner.OrgID+"/dept_legal"] = true
	p := e.principal(t, owner)

	bad := []InviteInput{
		{Email: "a@x.com", Role: RoleMember, DepartmentID: "dept_nope"},
		{Email: "a@x.com", Role: RoleMember, Profession: "astronaut"},
		{Email: "a@x.com", Role: RoleMember, Lead: true},
	}
	for _, in := range bad {
		var ve *ValidationError
		if _, err := e.svc.Invite(bg, p, in); !errors.As(err, &ve) {
			t.Fatalf("%+v: want a validation error, got %v", in, err)
		}
	}
	inv, err := e.svc.Invite(bg, p, InviteInput{Email: "luis@x.com", Role: RoleMember, DepartmentID: "dept_legal", Profession: "legal", Lead: true})
	if err != nil {
		t.Fatal(err)
	}
	if inv.DepartmentID != "dept_legal" || inv.Profession != "legal" || !inv.Lead {
		t.Fatalf("created: %+v", inv)
	}
	sess, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Name: "Luis", Password: goodPW}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := e.svc.MemberProfile(bg, owner.OrgID, sess.User.ID)
	if err != nil || prof.DepartmentID != "dept_legal" || prof.Profession != "legal" || !prof.Lead || prof.Via != ViaInvitation || prof.InvitedBy != owner.User.ID {
		t.Fatalf("profile: %+v %v", prof, err)
	}
}

func TestInvitationWithATeamNeedsTheTeamsChecker(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "boss@y.com", "Y")
	var ve *ValidationError
	if _, err := e.svc.Invite(bg, e.principal(t, owner), InviteInput{Email: "a@y.com", Role: RoleMember, DepartmentID: "dept_1"}); !errors.As(err, &ve) {
		t.Fatalf("want a validation error, got %v", err)
	}
	// Without a team the invitation works as always.
	if _, err := e.svc.Invite(bg, e.principal(t, owner), InviteInput{Email: "a@y.com", Role: RoleMember, Profession: "sales"}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveMemberProfileAndSetupDone(t *testing.T) {
	e := newEnv(t)
	s, _ := e.svc.Register(bg, RegisterInput{Email: "c@example.com", Password: goodPW, Name: "C D", OrgName: "CD", Audience: AudienceCompany, Profession: "direction"}, ClientMeta{})
	if err := e.svc.MarkOrgSetupDone(bg, s.OrgID); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.OrgSetup(bg, s.OrgID); st.SetupDoneAt.IsZero() {
		t.Fatal("setup not marked done")
	}
	p, _ := e.svc.MemberProfile(bg, s.OrgID, s.User.ID)
	p.DepartmentID = "dept_1"
	if err := e.svc.SaveMemberProfile(bg, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.MemberProfile(bg, s.OrgID, s.User.ID); got.DepartmentID != "dept_1" || got.UpdatedAt.IsZero() {
		t.Fatalf("saved: %+v", got)
	}
}

func TestTeamSignupOverHTTP(t *testing.T) {
	mm := &captureMailer{}
	fj := &fakeJoiner{org: "org-acme", role: RoleMember, on: true}
	depts := deptSet{}
	h := newHTTPEnv(t, HandlerConfig{}, func(c *Config) {
		c.RequireVerification, c.Mailer, c.AppURL, c.AutoJoin, c.DepartmentExists = true, mm, "https://app.example.com/", fj, depts.exists
	})
	r := chi.NewRouter()
	r.Mount("/auth", NewHandler(h.svc, HandlerConfig{}).Routes())
	r.Mount("/invitations", NewHandler(h.svc, HandlerConfig{}).InvitationRoutes())
	h.srv = r
	acmeOrg(t, h.env)

	if w := h.do("POST", "/auth/signup/check", "", map[string]any{"email": "ana@acme.io"}); w.Code != http.StatusOK || !jsonHas(w.Body.String(), `"join_org_name":"Acme"`) {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/auth/signup/check", "", map[string]any{"email": "ana@nowhere.com"}); w.Code != http.StatusOK || jsonHas(w.Body.String(), "join_org_name") {
		t.Fatalf("check other: %d %s", w.Code, w.Body)
	}
	w := h.do("POST", "/auth/register", "", map[string]any{"email": "eva@nowhere.com", "password": goodPW, "name": "Eva", "org_name": "Eva SL",
		"audience": "team", "profession": "accounting", "team_name": "Números", "locale": "es"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	sess, err := h.svc.Verify(bg, tokenIn(t, mm.last(t)), ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := h.svc.MemberProfile(bg, sess.OrgID, sess.User.ID); p.Profession != "accounting" || p.TeamName != "Números" {
		t.Fatalf("profile: %+v", p)
	}
	depts[sess.OrgID+"/dept_n"] = true
	w = h.do("POST", "/invitations", sess.AccessToken, map[string]any{"email": "leo@nowhere.com", "role": "member", "department_id": "dept_n", "profession": "accounting", "lead": false})
	if w.Code != http.StatusCreated || !jsonHas(w.Body.String(), `"department_id":"dept_n"`) || !jsonHas(w.Body.String(), `"profession":"accounting"`) {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
}
