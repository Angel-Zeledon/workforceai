package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Config configures a Service.
type Config struct {
	Store  Store
	Secret []byte // HMAC key for access tokens, >= 32 bytes
	// Issuer / Audience are embedded in and checked on every access token.
	Issuer   string // default "aiworkforce"
	Audience string // default "aiworkforce-api"

	AccessTTL  time.Duration // default 15m
	RefreshTTL time.Duration // default 30d

	MaxFailedAttempts int           // default 5
	LockDuration      time.Duration // default 15m

	PasswordParams PasswordParams

	// LoginLimiter (optional) throttles login attempts per e-mail address,
	// independently of whether the account exists (anti-enumeration and
	// anti-credential-stuffing). Defaults to 10 attempts / 15 minutes.
	LoginLimiter Limiter
	LoginLimit   Limit

	// TrustTokenClaims skips the per-request membership lookup and trusts the
	// role inside the JWT (stateless, but a demoted user keeps their old role
	// until the access token expires). Default false: role comes from the DB.
	TrustTokenClaims bool

	// DefaultOrgBudgetUSD is the budget assigned to newly registered orgs.
	DefaultOrgBudgetUSD float64

	// InvitationTTL is how long an invitation link stays valid (default 7 days).
	InvitationTTL time.Duration
	// Audit (optional) receives the invitation events for the audit trail.
	Audit func(ctx context.Context, e AuditEvent)

	Now func() time.Time // default time.Now (tests)
}

// Service implements registration, login, token lifecycle and member
// management on top of a Store.
type Service struct {
	cfg       Config
	store     Store
	signer    *TokenSigner
	hasher    PasswordHasher
	dummyHash string
}

// NewService validates cfg and builds a Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("auth: Store is required")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "aiworkforce"
	}
	if cfg.Audience == "" {
		cfg.Audience = "aiworkforce-api"
	}
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = 30 * 24 * time.Hour
	}
	if cfg.MaxFailedAttempts <= 0 {
		cfg.MaxFailedAttempts = 5
	}
	if cfg.LockDuration <= 0 {
		cfg.LockDuration = 15 * time.Minute
	}
	if cfg.LoginLimit.Max <= 0 || cfg.LoginLimit.Window <= 0 {
		cfg.LoginLimit = Limit{Max: 10, Window: 15 * time.Minute}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	signer, err := NewTokenSigner(cfg.Secret, cfg.Issuer, cfg.Audience)
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, store: cfg.Store, signer: signer, hasher: PasswordHasher{Params: cfg.PasswordParams}}
	// Hash for unknown accounts, so "no such user" costs as much as a bad password.
	s.dummyHash, err = s.hasher.Hash(randomHex(16))
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) now() time.Time { return s.cfg.Now() }

// ClientMeta is optional request metadata stored with refresh tokens.
type ClientMeta struct {
	UserAgent string
	IP        string
}

// UserView is the public part of a user.
type UserView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Session is returned by Register, Login and Refresh.
type Session struct {
	TokenType    string    `json:"token_type"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"` // seconds
	ExpiresAt    time.Time `json:"expires_at"`
	User         UserView  `json:"user"`
	OrgID        string    `json:"org_id"`
	Role         Role      `json:"role"`
}

type RegisterInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	OrgName  string `json:"org_name"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	OrgID    string `json:"org_id,omitempty"` // optional: pick one of the user's orgs
}

// Register creates an organization, its first user (role owner) and a session.
func (s *Service) Register(ctx context.Context, in RegisterInput, meta ClientMeta) (*Session, error) {
	email := NormalizeEmail(in.Email)
	in.Name, in.OrgName = strings.TrimSpace(in.Name), strings.TrimSpace(in.OrgName)
	ve := &ValidationError{}
	if m := ValidateEmail(email); m != "" {
		ve.add("email", m)
	}
	if m := ValidatePassword(in.Password, email); m != "" {
		ve.add("password", m)
	}
	if m := ValidateName(in.Name, 1); m != "" {
		ve.add("name", m)
	}
	if m := ValidateName(in.OrgName, 2); m != "" {
		ve.add("org_name", m)
	}
	if err := ve.orNil(); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	u := User{ID: uuid.NewString(), Email: email, Name: in.Name, PasswordHash: hash, CreatedAt: now}
	for attempt := 0; ; attempt++ {
		o := Org{ID: uuid.NewString(), Name: in.OrgName, Slug: slugify(in.OrgName) + "-" + randomHex(3), BudgetUSD: s.cfg.DefaultOrgBudgetUSD}
		m := Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: now}
		err = s.store.CreateAccount(ctx, u, o, m)
		if errors.Is(err, ErrSlugTaken) && attempt < 3 {
			continue
		}
		if err != nil {
			return nil, err
		}
		return s.issueSession(ctx, u, m, uuid.NewString(), meta)
	}
}

// Login verifies credentials and opens a session. Failures are counted per
// account; after MaxFailedAttempts the account is locked for LockDuration.
func (s *Service) Login(ctx context.Context, in LoginInput, meta ClientMeta) (*Session, error) {
	email := NormalizeEmail(in.Email)
	if ValidateEmail(email) != "" || len(in.Password) == 0 || len(in.Password) > 4*MaxPasswordLen {
		_, _, _ = s.hasher.Verify("x", s.dummyHash) // keep timing uniform
		return nil, ErrInvalidCredentials
	}
	if s.cfg.LoginLimiter != nil {
		res, err := s.cfg.LoginLimiter.Allow(ctx, "login:email:"+hashToken(email), s.cfg.LoginLimit)
		if err != nil {
			return nil, fmt.Errorf("auth: login limiter: %w", err)
		}
		if !res.Allowed {
			return nil, &RateLimitedError{RetryAfter: res.RetryAfter}
		}
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_, _, _ = s.hasher.Verify(in.Password, s.dummyHash)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if u.LockedUntil.After(now) {
		return nil, &LockedError{Until: u.LockedUntil}
	}
	ok, rehash, err := s.hasher.Verify(in.Password, u.PasswordHash)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if !ok {
		upd, ferr := s.store.RecordLoginFailure(ctx, u.ID, s.cfg.MaxFailedAttempts, s.cfg.LockDuration, now)
		if ferr != nil {
			return nil, ferr
		}
		if upd.LockedUntil.After(now) {
			return nil, &LockedError{Until: upd.LockedUntil}
		}
		return nil, ErrInvalidCredentials
	}
	if u.FailedAttempts > 0 || !u.LockedUntil.IsZero() {
		if err := s.store.ResetLoginFailures(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	if rehash {
		if h, err := s.hasher.Hash(in.Password); err == nil {
			_ = s.store.UpdatePasswordHash(ctx, u.ID, h)
		}
	}
	m, err := s.pickMembership(ctx, u.ID, in.OrgID)
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, u, m, uuid.NewString(), meta)
}

func (s *Service) pickMembership(ctx context.Context, userID, orgID string) (Membership, error) {
	if orgID != "" {
		m, err := s.store.GetMembership(ctx, orgID, userID)
		if errors.Is(err, ErrNotFound) {
			return Membership{}, ErrForbidden
		}
		return m, err
	}
	ms, err := s.store.MembershipsOf(ctx, userID)
	if err != nil {
		return Membership{}, err
	}
	if len(ms) == 0 {
		return Membership{}, ErrForbidden
	}
	return ms[0], nil
}

// Refresh exchanges a refresh token for a new access + refresh pair. The old
// refresh token is consumed; presenting it again revokes the whole family.
func (s *Service) Refresh(ctx context.Context, refreshToken string, meta ClientMeta) (*Session, error) {
	if refreshToken == "" || len(refreshToken) > 256 {
		return nil, ErrInvalidToken
	}
	plain, hash, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	old, err := s.store.RotateRefreshToken(ctx, hashToken(refreshToken), RefreshToken{
		ID: uuid.NewString(), TokenHash: hash, ExpiresAt: now.Add(s.cfg.RefreshTTL), CreatedAt: now,
		UserAgent: meta.UserAgent, IP: meta.IP,
	}, now)
	if err != nil {
		return nil, err
	}
	u, uerr := s.store.UserByID(ctx, old.UserID)
	m, merr := s.store.GetMembership(ctx, old.OrgID, old.UserID)
	if uerr != nil || merr != nil {
		// Membership revoked since the token was issued: kill the family.
		_ = s.store.RevokeFamilyOf(ctx, hash, now)
		return nil, ErrInvalidToken
	}
	access, exp, err := s.accessFor(u, m)
	if err != nil {
		return nil, err
	}
	return s.sessionOf(u, m, access, exp, plain), nil
}

// Logout revokes the refresh-token family of the given token. Idempotent.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" || len(refreshToken) > 256 {
		return nil
	}
	return s.store.RevokeFamilyOf(ctx, hashToken(refreshToken), s.now())
}

// LogoutAll revokes every refresh token of the user, in every organization.
func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	return s.store.RevokeUserTokens(ctx, userID, s.now())
}

// Authenticate resolves an access token to a Principal (user + org + role).
func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	c, err := s.signer.Verify(accessToken, s.now())
	if err != nil {
		return Principal{}, err
	}
	if s.cfg.TrustTokenClaims {
		if !c.Role.Valid() {
			return Principal{}, ErrInvalidToken
		}
		return Principal{UserID: c.Subject, OrgID: c.OrgID, Role: c.Role}, nil
	}
	m, err := s.store.GetMembership(ctx, c.OrgID, c.Subject)
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated // membership revoked or forged org
	}
	if err != nil {
		return Principal{}, err
	}
	return Principal{UserID: c.Subject, OrgID: c.OrgID, Role: m.Role}, nil
}

// Me returns the user behind a principal.
func (s *Service) Me(ctx context.Context, p Principal) (UserView, error) {
	u, err := s.store.UserByID(ctx, p.UserID)
	if err != nil {
		return UserView{}, err
	}
	return UserView{ID: u.ID, Email: u.Email, Name: u.Name}, nil
}

func (s *Service) accessFor(u User, m Membership) (string, time.Time, error) {
	now := s.now()
	exp := now.Add(s.cfg.AccessTTL)
	tok, err := s.signer.Sign(Claims{
		Subject: u.ID, OrgID: m.OrgID, Role: m.Role,
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: exp.Unix(), ID: uuid.NewString(),
	})
	return tok, exp, err
}

func (s *Service) sessionOf(u User, m Membership, access string, exp time.Time, refresh string) *Session {
	return &Session{
		TokenType: "Bearer", AccessToken: access, RefreshToken: refresh,
		ExpiresIn: int(s.cfg.AccessTTL.Seconds()), ExpiresAt: exp,
		User:  UserView{ID: u.ID, Email: u.Email, Name: u.Name},
		OrgID: m.OrgID, Role: m.Role,
	}
}

func (s *Service) issueSession(ctx context.Context, u User, m Membership, family string, meta ClientMeta) (*Session, error) {
	plain, hash, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.store.SaveRefreshToken(ctx, RefreshToken{
		ID: uuid.NewString(), UserID: u.ID, OrgID: m.OrgID, FamilyID: family, TokenHash: hash,
		ExpiresAt: now.Add(s.cfg.RefreshTTL), CreatedAt: now, UserAgent: meta.UserAgent, IP: meta.IP,
	}); err != nil {
		return nil, err
	}
	access, exp, err := s.accessFor(u, m)
	if err != nil {
		return nil, err
	}
	return s.sessionOf(u, m, access, exp, plain), nil
}

// ---- member management (all scoped to the actor's own organization) ----

// authorize re-reads the actor's current membership (never trusting a possibly
// stale Principal) and checks perm.
func (s *Service) authorize(ctx context.Context, actor Principal, perm Permission) (Membership, error) {
	if actor.UserID == "" || actor.OrgID == "" {
		return Membership{}, ErrUnauthenticated
	}
	m, err := s.store.GetMembership(ctx, actor.OrgID, actor.UserID)
	if errors.Is(err, ErrNotFound) {
		return Membership{}, ErrUnauthenticated
	}
	if err != nil {
		return Membership{}, err
	}
	if perm != "" && !m.Role.Has(perm) {
		return Membership{}, ErrForbidden
	}
	return m, nil
}

// ListMembers lists the actor's organization members.
func (s *Service) ListMembers(ctx context.Context, actor Principal) ([]Member, error) {
	if _, err := s.authorize(ctx, actor, PermMembersRead); err != nil {
		return nil, err
	}
	return s.store.ListMembers(ctx, actor.OrgID)
}

// AddMember adds an existing user (by e-mail) to the actor's organization.
// Callers can only grant roles strictly allowed by CanAssign.
func (s *Service) AddMember(ctx context.Context, actor Principal, email string, role Role) (Member, error) {
	am, err := s.authorize(ctx, actor, PermMembersInvite)
	if err != nil {
		return Member{}, err
	}
	if !role.Valid() {
		return Member{}, &ValidationError{Fields: map[string]string{"role": "unknown role"}}
	}
	if !CanAssign(am.Role, role) {
		return Member{}, ErrForbidden
	}
	u, err := s.store.UserByEmail(ctx, NormalizeEmail(email))
	if err != nil {
		return Member{}, err
	}
	now := s.now()
	if err := s.store.AddMembership(ctx, Membership{OrgID: actor.OrgID, UserID: u.ID, Role: role, CreatedAt: now}); err != nil {
		return Member{}, err
	}
	return Member{UserID: u.ID, Email: u.Email, Name: u.Name, Role: role, CreatedAt: now}, nil
}

// ChangeMemberRole changes a member's role inside the actor's organization.
func (s *Service) ChangeMemberRole(ctx context.Context, actor Principal, targetUserID string, role Role) error {
	am, err := s.authorize(ctx, actor, PermMembersManage)
	if err != nil {
		return err
	}
	if !role.Valid() {
		return &ValidationError{Fields: map[string]string{"role": "unknown role"}}
	}
	// Lookup is scoped to the actor's org: users of other orgs are "not found".
	tm, err := s.store.GetMembership(ctx, actor.OrgID, targetUserID)
	if err != nil {
		return err
	}
	if targetUserID == actor.UserID {
		// Only owners may change their own role (e.g. hand over ownership);
		// admins can never promote themselves.
		if am.Role != RoleOwner {
			return ErrForbidden
		}
	} else if !CanManage(am.Role, tm.Role) {
		return ErrForbidden
	}
	if !CanAssign(am.Role, role) {
		return ErrForbidden
	}
	return s.store.SetMemberRole(ctx, actor.OrgID, targetUserID, role)
}

// RemoveMember removes a member from the actor's organization. Any member can
// remove themselves (leave); removing others requires members:manage and
// outranking the target. The last owner can never be removed.
func (s *Service) RemoveMember(ctx context.Context, actor Principal, targetUserID string) error {
	am, err := s.authorize(ctx, actor, "")
	if err != nil {
		return err
	}
	tm, err := s.store.GetMembership(ctx, actor.OrgID, targetUserID)
	if err != nil {
		return err
	}
	if targetUserID != actor.UserID {
		if !am.Role.Has(PermMembersManage) || !CanManage(am.Role, tm.Role) {
			return ErrForbidden
		}
	}
	if err := s.store.RemoveMember(ctx, actor.OrgID, targetUserID); err != nil {
		return err
	}
	return s.store.RevokeOrgUserTokens(ctx, actor.OrgID, targetUserID, s.now())
}
