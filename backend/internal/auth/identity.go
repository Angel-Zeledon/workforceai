package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Enterprise identity hooks (docs/architecture/identity.md): the organization policy (require MFA, enforce SSO), federated
// sign-in for the SSO and SCIM modules, and the forced-enrolment login flow. The policy itself is stored by internal/sso;
// this package only asks for it through PolicyProvider, so the dependency points one way (sso -> auth).

// OrgPolicy is what an organization demands of its members when they sign in.
type OrgPolicy struct {
	// RequireMFA: members must have a second factor. A password login without one is not given a session; it is given an
	// enrolment challenge instead. Federated (SSO) logins are exempt: the identity provider owns that decision.
	RequireMFA bool
	// EnforceSSO: members sign in through the identity provider; password login is refused. Owners keep the password as a
	// break-glass path so a broken IdP configuration can never lock the organization out.
	EnforceSSO bool
}

// PolicyProvider supplies the policy of an organization. A nil provider means "no policy".
type PolicyProvider interface {
	OrgPolicy(ctx context.Context, orgID string) (OrgPolicy, error)
}

var (
	// ErrSSORequired: the organization enforces single sign-on and this login used a password.
	ErrSSORequired = errors.New("auth: this organization requires single sign-on")
	// ErrMFARequiredByOrg: an organization the user belongs to requires two-factor, so it cannot be turned off.
	ErrMFARequiredByOrg = errors.New("auth: an organization you belong to requires two-factor authentication")
)

// MFAEnrollmentRequiredError is returned (instead of a session) when the organization requires a second factor and the
// user has none. Token lets the client enrol without a session; it is empty when the caller is already signed in.
type MFAEnrollmentRequiredError struct{ Token string }

func (e *MFAEnrollmentRequiredError) Error() string { return "auth: two-factor enrolment required" }

const challengeEnroll = "enroll"

func (s *Service) policyOf(ctx context.Context, orgID string) (OrgPolicy, error) {
	if s.cfg.Policy == nil {
		return OrgPolicy{}, nil
	}
	return s.cfg.Policy.OrgPolicy(ctx, orgID)
}

// enforcePolicy applies the organization policy to a password login, or to an organization switch (passwordLogin false).
func (s *Service) enforcePolicy(ctx context.Context, u User, m Membership, passwordLogin bool) error {
	pol, err := s.policyOf(ctx, m.OrgID)
	if err != nil {
		return err // fail closed: an unreadable policy must not become "no policy"
	}
	if passwordLogin && pol.EnforceSSO && m.Role != RoleOwner {
		return ErrSSORequired
	}
	if pol.RequireMFA && !s.MFAEnabled(ctx, u.ID) {
		e := &MFAEnrollmentRequiredError{}
		if passwordLogin {
			e.Token = s.newChallengeP(u.ID, m.OrgID, challengeEnroll, s.now())
		}
		return e
	}
	return nil
}

// mfaRequiredElsewhere reports whether any organization of the user requires a second factor.
func (s *Service) mfaRequiredElsewhere(ctx context.Context, userID string) (bool, error) {
	if s.cfg.Policy == nil {
		return false, nil
	}
	ms, err := s.store.MembershipsOf(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		pol, err := s.policyOf(ctx, m.OrgID)
		if err != nil {
			return false, err
		}
		if pol.RequireMFA {
			return true, nil
		}
	}
	return false, nil
}

// MFAEnrollSetup starts enrolment for a user who was stopped at login by the organization policy.
func (s *Service) MFAEnrollSetup(ctx context.Context, token string) (*MFASetupInfo, error) {
	userID, _, err := s.parseChallengeP(token, challengeEnroll, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.enrollLimit(ctx, userID); err != nil {
		return nil, err
	}
	return s.MFASetup(ctx, userID)
}

// MFAEnrollComplete confirms the first code, turns the second factor on and opens the session that login withheld.
func (s *Service) MFAEnrollComplete(ctx context.Context, token, code string, meta ClientMeta) (*Session, []string, error) {
	userID, orgID, err := s.parseChallengeP(token, challengeEnroll, s.now())
	if err != nil {
		return nil, nil, err
	}
	if err := s.enrollLimit(ctx, userID); err != nil {
		return nil, nil, err
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	if u.LockedUntil.After(s.now()) {
		return nil, nil, &LockedError{Until: u.LockedUntil}
	}
	codes, err := s.MFAEnable(ctx, userID, code)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.pickMembership(ctx, userID, orgID)
	if err != nil {
		return nil, nil, err
	}
	sess, err := s.issueSession(ctx, u, m, uuid.NewString(), meta)
	return sess, codes, err
}

func (s *Service) enrollLimit(ctx context.Context, userID string) error {
	if s.cfg.LoginLimiter == nil {
		return nil
	}
	res, err := s.cfg.LoginLimiter.Allow(ctx, "mfa-enroll:user:"+hashToken(userID), s.cfg.LoginLimit)
	if err != nil {
		return err
	}
	if !res.Allowed {
		return &RateLimitedError{RetryAfter: res.RetryAfter}
	}
	return nil
}

// ---- federated sign-in (SSO) ----

// FederatedIdentity is a sign-in already proven by an identity provider.
type FederatedIdentity struct {
	OrgID    string `json:"org_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Provider string `json:"provider"` // for the audit trail, e.g. "oidc"
	// Role given to a NEW membership. Owner is never granted this way.
	Role Role `json:"role"`
	// JIT lets the sign-in create the account and the membership when missing. When false the person must already belong.
	JIT bool `json:"jit"`
}

// SSOExchanger redeems the single-use code the SSO callback hands the browser (internal/sso implements it).
type SSOExchanger interface {
	Exchange(ctx context.Context, code string) (FederatedIdentity, error)
}

// SignInFederated opens a session for an identity the IdP vouched for. The caller (internal/sso) has already verified the
// token, the nonce and that the e-mail domain is verified for the organization; this method only maps it to an account.
func (s *Service) SignInFederated(ctx context.Context, id FederatedIdentity, meta ClientMeta) (*Session, error) {
	email := NormalizeEmail(id.Email)
	if ValidateEmail(email) != "" || id.OrgID == "" {
		return nil, ErrForbidden
	}
	role := id.Role
	if !role.Valid() || role == RoleOwner {
		role = RoleMember
	}
	now := s.now()
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		if !id.JIT {
			return nil, ErrForbidden
		}
		name := strings.TrimSpace(id.Name)
		if ValidateName(name, 1) != "" {
			name = strings.SplitN(email, "@", 2)[0]
		}
		// The password is random and never revealed: the account can only be entered through its IdP (or a reset link).
		hash, herr := s.hasher.Hash(randomHex(32))
		if herr != nil {
			return nil, herr
		}
		u = User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: hash, CreatedAt: now, VerifiedAt: now}
		if err = s.store.CreateUser(ctx, u); errors.Is(err, ErrEmailTaken) { // lost a race: use the winner
			u, err = s.store.UserByEmail(ctx, email)
		}
	}
	if err != nil {
		return nil, err
	}
	if trialExpired(u, now) {
		return nil, ErrTrialExpired
	}
	m, err := s.store.GetMembership(ctx, id.OrgID, u.ID)
	if errors.Is(err, ErrNotFound) {
		if !id.JIT {
			return nil, ErrForbidden
		}
		m = Membership{OrgID: id.OrgID, UserID: u.ID, Role: role, CreatedAt: now}
		if err = s.store.AddMembership(ctx, m); errors.Is(err, ErrAlreadyMember) {
			m, err = s.store.GetMembership(ctx, id.OrgID, u.ID)
		}
	}
	if err != nil {
		return nil, err
	}
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, AuditEvent{OrgID: id.OrgID, Actor: u.ID, Action: "user.sso_login", Entity: "user", EntityID: u.ID,
			Details: map[string]any{"provider": id.Provider}})
	}
	return s.issueSession(ctx, u, m, uuid.NewString(), meta)
}
