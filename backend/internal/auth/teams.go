package auth

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/roles"
)

// E1 "Alta y agentes por rol" (docs/architecture/equipos-oficinas.md §6): the service side of the team sign-up. The
// records written here only describe the person and the organization; the API layer turns them into agents, teams and
// welcomes, and nothing here grants a permission.

// maxTeamName bounds a team (department) name typed at sign-up or in an invitation.
const maxTeamName = 80

// SignupOptions tells the sign-up form what an address can do before the account exists.
type SignupOptions struct {
	// JoinOrgName is the company the person can enter (its verified domain lets this address in on its own) instead
	// of creating another organization. Empty = nothing to join.
	JoinOrgName string `json:"join_org_name,omitempty"`
}

// SignupCheck answers whether an address can join an existing company by its verified domain. It says so only when
// the company turned auto-join on and e-mail verification is on (otherwise the address could never be proven), and it
// reveals the company's name only: nothing about its people.
func (s *Service) SignupCheck(ctx context.Context, email string) (SignupOptions, error) {
	org, ok := s.joinableOrg(ctx, NormalizeEmail(email))
	if !ok {
		return SignupOptions{}, nil
	}
	name, err := s.store.OrgName(ctx, org)
	if err != nil {
		return SignupOptions{}, nil
	}
	return SignupOptions{JoinOrgName: name}, nil
}

// joinableOrg is the organization that would let this (not yet proven) address in once it is verified.
func (s *Service) joinableOrg(ctx context.Context, email string) (string, bool) {
	if !s.verifyEnabled() || s.cfg.AutoJoin == nil || ValidateEmail(email) != "" {
		return "", false
	}
	org, _, ok, err := s.cfg.AutoJoin.AutoJoinFor(ctx, email)
	if err != nil || !ok || org == "" {
		return "", false
	}
	return org, true
}

// validateTeamFields checks the optional E1 fields of a sign-up.
func validateTeamFields(ve *ValidationError, in *RegisterInput) {
	in.TeamName = strings.Join(strings.Fields(in.TeamName), " ")
	if in.Audience != "" && !in.Audience.Valid() {
		ve.add("audience", "must be solo, team or company")
	}
	if in.Profession != "" && !roles.ValidProfession(in.Profession) {
		ve.add("profession", "unknown profession")
	}
	if utf8.RuneCountInString(in.TeamName) > maxTeamName {
		ve.add("team_name", "too long")
	}
}

// teamProfile is the owner's profile of an organization created by the new sign-up.
func (s *Service) writeSignupProfile(ctx context.Context, orgID, userID string, aud Audience, profession, teamName, locale string) {
	now := s.now()
	if profession == "" {
		profession = roles.ProfessionOther
	}
	if aud != AudienceTeam {
		teamName = "" // a personal office has no team; a company sets its teams up in the welcome
	} else if teamName == "" {
		p, _ := roles.ProfessionOf(profession)
		teamName = p.TeamName(locale)
	}
	if err := s.store.PutOrgSetup(ctx, OrgSetup{OrgID: orgID, Audience: aud, Locale: normLocaleTeams(locale), CreatedAt: now}); err != nil {
		s.cfg.Logf("auth: org setup not saved: %v", err)
		return
	}
	if err := s.store.PutMemberProfile(ctx, MemberProfile{OrgID: orgID, UserID: userID, Profession: profession, TeamName: teamName,
		Lead: aud != AudienceSolo, Via: ViaSignup, CreatedAt: now, UpdatedAt: now}); err != nil {
		s.cfg.Logf("auth: member profile not saved: %v", err)
	}
}

// normLocaleTeams keeps es, en and pt (the three languages of the team names); anything else is Spanish.
func normLocaleTeams(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	switch {
	case strings.HasPrefix(l, "en"):
		return "en"
	case strings.HasPrefix(l, "pt"):
		return "pt"
	}
	return "es"
}

// registerToJoin creates the account WITHOUT an organization and keeps what the person chose; the verification link
// then lets them into the company (Verify), or creates their own organization if the company no longer lets them in.
func (s *Service) registerToJoin(ctx context.Context, u User, in RegisterInput) error {
	if err := s.store.CreateUser(ctx, u); err != nil {
		return err
	}
	aud := in.Audience
	if !aud.Valid() {
		aud = AudienceSolo
	}
	orgName := in.OrgName
	if orgName == "" {
		orgName = in.Name
	}
	if err := s.store.PutSignupIntent(ctx, SignupIntent{UserID: u.ID, Audience: aud, Profession: in.Profession, OrgName: orgName,
		TeamName: in.TeamName, Locale: normLocaleTeams(in.Locale), CreatedAt: s.now()}); err != nil {
		return err
	}
	sent := true
	if err := s.sendVerification(ctx, u, in.Locale); err != nil {
		sent = false
		s.cfg.Logf("auth: verification e-mail failed for a new account: %v", err)
	}
	return &VerificationRequiredError{Email: u.Email, EmailSent: sent}
}

// finishSignupIntent runs when an address that registered to join is proven: a profile in the company it joined, or
// its own organization (with the team sign-up) when it did not join any.
func (s *Service) finishSignupIntent(ctx context.Context, u User, joined string) {
	in, err := s.store.TakeSignupIntent(ctx, u.ID)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		s.cfg.Logf("auth: sign-up intent: %v", err)
		return
	}
	if joined != "" {
		s.saveJoinProfile(ctx, joined, u.ID, in.Profession, ViaDomain)
		return
	}
	if ms, err := s.store.MembershipsOf(ctx, u.ID); err == nil && len(ms) > 0 {
		return // already somewhere (e.g. an invitation was accepted meanwhile)
	}
	name := in.OrgName
	if ValidateName(name, 2) != "" {
		name = u.Name
	}
	if ValidateName(name, 2) != "" {
		name = "Office " + randomHex(2)
	}
	for attempt := 0; attempt < 4; attempt++ {
		o := Org{ID: uuid.NewString(), Name: name, Slug: slugify(name) + "-" + randomHex(3), BudgetUSD: s.cfg.DefaultOrgBudgetUSD}
		err = s.store.CreateOrg(ctx, o, Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: s.now()})
		if errors.Is(err, ErrSlugTaken) {
			continue
		}
		if err != nil {
			s.cfg.Logf("auth: own organization after a failed join: %v", err)
			return
		}
		s.writeSignupProfile(ctx, o.ID, u.ID, in.Audience, in.Profession, in.TeamName, in.Locale)
		return
	}
}

// saveJoinProfile records the profession of a person who joined by domain or SSO, keeping a team they already have.
func (s *Service) saveJoinProfile(ctx context.Context, org, userID, profession, via string) {
	now := s.now()
	p, err := s.store.GetMemberProfile(ctx, org, userID)
	if err != nil {
		p = MemberProfile{OrgID: org, UserID: userID, Via: via, CreatedAt: now}
	}
	if profession != "" && roles.ValidProfession(profession) {
		p.Profession = profession
	}
	p.UpdatedAt = now
	if err := s.store.PutMemberProfile(ctx, p); err != nil {
		s.cfg.Logf("auth: member profile not saved: %v", err)
	}
}

// InviteInput is an invitation with its optional team (department), profession and "responsible of the team" mark.
type InviteInput struct {
	Email        string `json:"email"`
	Role         Role   `json:"role"`
	DepartmentID string `json:"department_id,omitempty"`
	Profession   string `json:"profession,omitempty"`
	Lead         bool   `json:"lead,omitempty"`
}

func (s *Service) validateInviteTeam(ctx context.Context, org string, in *InviteInput, ve *ValidationError) {
	in.DepartmentID = strings.TrimSpace(in.DepartmentID)
	if in.Profession != "" && !roles.ValidProfession(in.Profession) {
		ve.add("profession", "unknown profession")
	}
	switch {
	case in.DepartmentID == "":
		if in.Lead {
			ve.add("lead", "a responsible needs a team")
		}
	case s.cfg.DepartmentExists == nil:
		ve.add("department_id", "teams are not available")
	case len(in.DepartmentID) > 80 || !s.cfg.DepartmentExists(ctx, org, in.DepartmentID):
		ve.add("department_id", "unknown team")
	}
}

// profileFromInvitation is what the person who accepts an invitation gets: the team, profession and mark it carried.
func (s *Service) profileFromInvitation(ctx context.Context, inv Invitation, userID string) {
	now := s.now()
	p := MemberProfile{OrgID: inv.OrgID, UserID: userID, Profession: inv.Profession, DepartmentID: inv.DepartmentID, Lead: inv.Lead,
		Via: ViaInvitation, InvitedBy: inv.InvitedBy, CreatedAt: now, UpdatedAt: now}
	if err := s.store.PutMemberProfile(ctx, p); err != nil {
		s.cfg.Logf("auth: member profile not saved: %v", err)
	}
}

// ---- reads and writes for the API layer (no permission check here: the API routes decide who may call them) ----

// OrgSetup returns the team sign-up of an organization (ErrNotFound for classic organizations).
func (s *Service) OrgSetup(ctx context.Context, org string) (OrgSetup, error) {
	return s.store.GetOrgSetup(ctx, org)
}

// MarkOrgSetupDone records that the responsible finished (or skipped) setting up the teams.
func (s *Service) MarkOrgSetupDone(ctx context.Context, org string) error {
	return s.store.MarkOrgSetupDone(ctx, org, s.now())
}

// MemberProfile returns the profile of a person in an organization (ErrNotFound when there is none).
func (s *Service) MemberProfile(ctx context.Context, org, userID string) (MemberProfile, error) {
	return s.store.GetMemberProfile(ctx, org, userID)
}

// MemberProfiles lists the profiles of an organization.
func (s *Service) MemberProfiles(ctx context.Context, org string) ([]MemberProfile, error) {
	return s.store.ListMemberProfiles(ctx, org)
}

// SaveMemberProfile writes a profile (the API layer sets the team and marks it provisioned). The person must be a
// member of the organization; the profession must be known.
func (s *Service) SaveMemberProfile(ctx context.Context, p MemberProfile) error {
	if _, err := s.store.GetMembership(ctx, p.OrgID, p.UserID); err != nil {
		return err
	}
	if p.Profession != "" && !roles.ValidProfession(p.Profession) {
		return &ValidationError{Fields: map[string]string{"profession": "unknown profession"}}
	}
	now := s.now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	return s.store.PutMemberProfile(ctx, p)
}

// Directory lists the members of an organization with their names (for welcomes and team pages; no permission check).
func (s *Service) Directory(ctx context.Context, org string) ([]Member, error) {
	return s.store.ListMembers(ctx, org)
}
