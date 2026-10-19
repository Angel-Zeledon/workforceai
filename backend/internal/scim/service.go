package scim

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"aiworkforce/backend/internal/auth"
	"github.com/google/uuid"
)

// Options configures a Service.
type Options struct {
	Store Store
	Auth  auth.Store
	// DomainVerified reports whether the e-mail's domain is verified for the organization. SCIM refuses to provision
	// addresses outside the organization's verified domains (a leaked token must not be able to pull arbitrary people in).
	DomainVerified func(ctx context.Context, orgID, email string) bool
	Audit          func(ctx context.Context, orgID, actor, action string, details map[string]any)
	Now            func() time.Time
}

// Service holds the provisioning logic.
type Service struct{ o Options }

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Auth == nil {
		return nil, errors.New("scim: Store and Auth are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.DomainVerified == nil {
		o.DomainVerified = func(context.Context, string, string) bool { return false } // fail closed
	}
	if o.Audit == nil {
		o.Audit = func(context.Context, string, string, string, map[string]any) {}
	}
	return &Service{o: o}, nil
}

// Error carries the SCIM status and scimType (RFC 7644 section 3.12).
type Error struct {
	Status  int
	Type    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func errf(status int, typ, msg string) *Error { return &Error{Status: status, Type: typ, Message: msg} }

var (
	errNotFound   = errf(404, "", "resource not found")
	errUnique     = errf(409, "uniqueness", "a user with that userName already exists")
	errMutability = errf(403, "mutability", "this resource cannot be changed through SCIM")
)

// ---- token ----

const tokenPrefix = "scim_"

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

// RotateToken mints the organization's SCIM token, replacing the previous one. The plaintext is returned once.
func (s *Service) RotateToken(ctx context.Context, orgID, actor string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	if err := s.o.Store.PutToken(ctx, orgID, hashToken(tok), actor, s.o.Now().UTC()); err != nil {
		return "", err
	}
	s.o.Audit(ctx, orgID, actor, "scim.token_rotated", nil)
	return tok, nil
}

// RevokeToken switches provisioning off.
func (s *Service) RevokeToken(ctx context.Context, orgID, actor string) error {
	if err := s.o.Store.DeleteToken(ctx, orgID); err != nil {
		return err
	}
	s.o.Audit(ctx, orgID, actor, "scim.token_revoked", nil)
	return nil
}

// TokenInfo says whether a token exists and when it was last used.
func (s *Service) TokenInfo(ctx context.Context, orgID string) (TokenInfo, error) {
	return s.o.Store.TokenInfo(ctx, orgID)
}

// Authenticate resolves a bearer token to its organization.
func (s *Service) Authenticate(ctx context.Context, bearer string) (string, error) {
	if !strings.HasPrefix(bearer, tokenPrefix) || len(bearer) > 128 {
		return "", ErrNotFound
	}
	h := hashToken(bearer)
	org, err := s.o.Store.OrgForToken(ctx, h)
	if err != nil {
		return "", err
	}
	_ = s.o.Store.TouchToken(ctx, h, s.o.Now().UTC())
	return org, nil
}

// Deactivated is the sso.Options.Gate: an account SCIM deprovisioned cannot come back through SSO just-in-time creation.
func (s *Service) Deactivated(ctx context.Context, orgID, email string) error {
	u, err := s.o.Store.FindByUserName(ctx, orgID, email)
	if err == nil && !u.Active {
		return errors.New("scim: account deactivated")
	}
	return nil
}

// ---- users ----

// UserInput is the writable part of a SCIM User.
type UserInput struct {
	UserName    string
	ExternalID  string
	DisplayName string
	Active      *bool
	Role        auth.Role // "" = member
}

// View is a provisioned user joined with the account's name.
type View struct {
	User
	Name string
}

func validRole(r auth.Role) bool {
	return r == auth.RoleAdmin || r == auth.RoleMember || r == auth.RoleViewer
}

func (s *Service) view(ctx context.Context, u User) View {
	v := View{User: u, Name: u.UserName}
	if acc, err := s.o.Auth.UserByID(ctx, u.UserID); err == nil {
		v.Name = acc.Name
	}
	return v
}

func normEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", errf(400, "invalidValue", "userName must be an e-mail address")
	}
	return s, nil
}

// Create provisions (or re-activates) a user.
func (s *Service) Create(ctx context.Context, orgID, actor string, in UserInput) (View, error) {
	email, err := normEmail(in.UserName)
	if err != nil {
		return View{}, err
	}
	if !s.o.DomainVerified(ctx, orgID, email) {
		return View{}, errf(400, "invalidValue", "the e-mail domain is not verified for this organization")
	}
	role := in.Role
	if role == "" {
		role = auth.RoleMember
	}
	if !validRole(role) {
		return View{}, errf(400, "invalidValue", "role must be admin, member or viewer")
	}
	active := in.Active == nil || *in.Active
	now := s.o.Now().UTC()

	prev, perr := s.o.Store.FindByUserName(ctx, orgID, email)
	if perr == nil && prev.Active {
		return View{}, errUnique
	}
	var userID string
	if perr == nil {
		userID = prev.UserID
	} else {
		acc, aerr := s.o.Auth.UserByEmail(ctx, email)
		switch {
		case errors.Is(aerr, auth.ErrNotFound):
			hash, herr := auth.PasswordHasher{}.Hash(uuid.NewString() + uuid.NewString())
			if herr != nil {
				return View{}, herr
			}
			name := strings.TrimSpace(in.DisplayName)
			if auth.ValidateName(name, 1) != "" {
				name = strings.SplitN(email, "@", 2)[0]
			}
			acc = auth.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: hash, CreatedAt: now, VerifiedAt: now}
			if cerr := s.o.Auth.CreateUser(ctx, acc); cerr != nil && !errors.Is(cerr, auth.ErrEmailTaken) {
				return View{}, cerr
			} else if cerr != nil {
				if acc, aerr = s.o.Auth.UserByEmail(ctx, email); aerr != nil {
					return View{}, aerr
				}
			}
		case aerr != nil:
			return View{}, aerr
		}
		userID = acc.ID
		// An existing member keeps their membership; an owner is never adopted (SCIM cannot manage owners).
		if m, merr := s.o.Auth.GetMembership(ctx, orgID, userID); merr == nil {
			if m.Role == auth.RoleOwner {
				return View{}, errUnique
			}
			role = m.Role
		}
	}
	u := User{OrgID: orgID, UserID: userID, ExternalID: in.ExternalID, UserName: email, Active: active, Role: role, CreatedAt: now, UpdatedAt: now}
	if perr == nil {
		u.CreatedAt = prev.CreatedAt
	}
	if active {
		if err := s.addMember(ctx, u); err != nil {
			return View{}, err
		}
	}
	if err := s.o.Store.PutUser(ctx, u); err != nil {
		return View{}, err
	}
	s.o.Audit(ctx, orgID, actor, "scim.user_created", map[string]any{"user_id": userID, "role": string(role), "active": active})
	return s.view(ctx, u), nil
}

func (s *Service) addMember(ctx context.Context, u User) error {
	err := s.o.Auth.AddMembership(ctx, auth.Membership{OrgID: u.OrgID, UserID: u.UserID, Role: u.Role, CreatedAt: s.o.Now().UTC()})
	if errors.Is(err, auth.ErrAlreadyMember) {
		m, gerr := s.o.Auth.GetMembership(ctx, u.OrgID, u.UserID)
		if gerr == nil && m.Role != u.Role && m.Role != auth.RoleOwner {
			return s.o.Auth.SetMemberRole(ctx, u.OrgID, u.UserID, u.Role)
		}
		return nil
	}
	return err
}

// Get returns one provisioned user.
func (s *Service) Get(ctx context.Context, orgID, id string) (View, error) {
	u, err := s.o.Store.GetUser(ctx, orgID, id)
	if err != nil {
		return View{}, errNotFound
	}
	return s.view(ctx, u), nil
}

// List returns provisioned users, optionally only the one whose userName equals filterUserName.
func (s *Service) List(ctx context.Context, orgID, filterUserName string) ([]View, error) {
	us, err := s.o.Store.ListUsers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var out []View
	for _, u := range us {
		if filterUserName != "" && !strings.EqualFold(u.UserName, filterUserName) {
			continue
		}
		out = append(out, s.view(ctx, u))
	}
	return out, nil
}

// SetActive activates or deactivates a user. Deactivating removes the membership and ends every session in the
// organization at once; the record is kept so the account can be re-activated and SSO cannot silently re-create it.
func (s *Service) SetActive(ctx context.Context, orgID, actor, id string, active bool) (View, error) {
	u, err := s.o.Store.GetUser(ctx, orgID, id)
	if err != nil {
		return View{}, errNotFound
	}
	if u.Active == active {
		return s.view(ctx, u), nil
	}
	if active {
		u.Active = true
		if err := s.addMember(ctx, u); err != nil {
			return View{}, err
		}
	} else {
		m, merr := s.o.Auth.GetMembership(ctx, orgID, id)
		if merr == nil && m.Role == auth.RoleOwner {
			return View{}, errMutability
		}
		if rerr := s.o.Auth.RemoveMember(ctx, orgID, id); rerr != nil && !errors.Is(rerr, auth.ErrNotFound) {
			return View{}, rerr
		}
		_ = s.o.Auth.RevokeOrgUserTokens(ctx, orgID, id, s.o.Now())
		u.Active = false
	}
	u.UpdatedAt = s.o.Now().UTC()
	if err := s.o.Store.PutUser(ctx, u); err != nil {
		return View{}, err
	}
	act := "scim.user_deactivated"
	if active {
		act = "scim.user_activated"
	}
	s.o.Audit(ctx, orgID, actor, act, map[string]any{"user_id": id})
	return s.view(ctx, u), nil
}

// Replace applies a PUT: active and externalId follow the request; userName cannot change (it is the account's e-mail).
func (s *Service) Replace(ctx context.Context, orgID, actor, id string, in UserInput) (View, error) {
	u, err := s.o.Store.GetUser(ctx, orgID, id)
	if err != nil {
		return View{}, errNotFound
	}
	if in.UserName != "" && !strings.EqualFold(strings.TrimSpace(in.UserName), u.UserName) {
		return View{}, errf(400, "mutability", "userName cannot be changed")
	}
	if in.ExternalID != u.ExternalID {
		u.ExternalID = in.ExternalID
		u.UpdatedAt = s.o.Now().UTC()
		if err := s.o.Store.PutUser(ctx, u); err != nil {
			return View{}, err
		}
	}
	if in.Role != "" && in.Role != u.Role {
		if _, err := s.SetRole(ctx, orgID, actor, id, in.Role); err != nil {
			return View{}, err
		}
	}
	if in.Active != nil {
		return s.SetActive(ctx, orgID, actor, id, *in.Active)
	}
	return s.Get(ctx, orgID, id)
}

// SetRole maps a user to one of the existing roles. The owner role is never reachable through SCIM.
func (s *Service) SetRole(ctx context.Context, orgID, actor, id string, role auth.Role) (View, error) {
	if !validRole(role) {
		return View{}, errf(400, "invalidValue", "role must be admin, member or viewer")
	}
	u, err := s.o.Store.GetUser(ctx, orgID, id)
	if err != nil {
		return View{}, errNotFound
	}
	if m, merr := s.o.Auth.GetMembership(ctx, orgID, id); merr == nil && m.Role == auth.RoleOwner {
		return View{}, errMutability
	}
	u.Role, u.UpdatedAt = role, s.o.Now().UTC()
	if u.Active {
		if err := s.o.Auth.SetMemberRole(ctx, orgID, id, role); err != nil && !errors.Is(err, auth.ErrNotFound) {
			return View{}, err
		}
	}
	if err := s.o.Store.PutUser(ctx, u); err != nil {
		return View{}, err
	}
	s.o.Audit(ctx, orgID, actor, "scim.user_role_changed", map[string]any{"user_id": id, "role": string(role)})
	return s.view(ctx, u), nil
}

// ---- groups: one fixed group per assignable role ----

// Roles lists the groups SCIM exposes.
var Roles = []auth.Role{auth.RoleAdmin, auth.RoleMember, auth.RoleViewer}

// Group is a role and the provisioned users who currently hold it.
type Group struct {
	Role    auth.Role
	Members []View
}

// Groups returns the three role groups.
func (s *Service) Groups(ctx context.Context, orgID string) ([]Group, error) {
	us, err := s.o.Store.ListUsers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]Group, len(Roles))
	for i, r := range Roles {
		out[i].Role = r
		for _, u := range us {
			if u.Active && u.Role == r {
				out[i].Members = append(out[i].Members, s.view(ctx, u))
			}
		}
	}
	return out, nil
}

// GroupByID returns one role group (the id is the role name).
func (s *Service) GroupByID(ctx context.Context, orgID, id string) (Group, error) {
	gs, err := s.Groups(ctx, orgID)
	if err != nil {
		return Group{}, err
	}
	for _, g := range gs {
		if string(g.Role) == id {
			return g, nil
		}
	}
	return Group{}, errNotFound
}

// AddToGroup gives the users the group's role.
func (s *Service) AddToGroup(ctx context.Context, orgID, actor string, role auth.Role, userIDs []string) error {
	for _, id := range userIDs {
		if _, err := s.SetRole(ctx, orgID, actor, id, role); err != nil {
			return err
		}
	}
	return nil
}

// RemoveFromGroup demotes the users to the least-privileged role (viewer); removing someone from "viewer" changes nothing.
func (s *Service) RemoveFromGroup(ctx context.Context, orgID, actor string, role auth.Role, userIDs []string) error {
	for _, id := range userIDs {
		u, err := s.o.Store.GetUser(ctx, orgID, id)
		if err != nil {
			return errNotFound
		}
		if u.Role == role && role != auth.RoleViewer {
			if _, err := s.SetRole(ctx, orgID, actor, id, auth.RoleViewer); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReplaceGroupMembers makes the group's members exactly userIDs.
func (s *Service) ReplaceGroupMembers(ctx context.Context, orgID, actor string, role auth.Role, userIDs []string) error {
	g, err := s.GroupByID(ctx, orgID, string(role))
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range userIDs {
		want[id] = true
	}
	var drop []string
	for _, m := range g.Members {
		if !want[m.UserID] {
			drop = append(drop, m.UserID)
		}
	}
	if err := s.AddToGroup(ctx, orgID, actor, role, userIDs); err != nil {
		return err
	}
	return s.RemoveFromGroup(ctx, orgID, actor, role, drop)
}
