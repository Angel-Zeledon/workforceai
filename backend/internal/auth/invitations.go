package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Invitation lets a person join an organization with a given role. Only the
// SHA-256 of the token is stored; the plaintext is returned once, at creation,
// for the inviter to share as a link (no e-mail is sent by the server yet).
type Invitation struct {
	ID         string    `json:"id"`
	OrgID      string    `json:"org_id"`
	Email      string    `json:"email"`
	Role       Role      `json:"role"`
	TokenHash  string    `json:"-"`
	InvitedBy  string    `json:"invited_by"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcceptedAt time.Time `json:"-"`
	AcceptedBy string    `json:"accepted_by,omitempty"`
	RevokedAt  time.Time `json:"-"`
}

// Invitation states (derived, never stored).
const (
	InvitationPending  = "pending"
	InvitationAccepted = "accepted"
	InvitationRevoked  = "revoked"
	InvitationExpired  = "expired"
)

// Status derives the state of the invitation at `now`.
func (i Invitation) Status(now time.Time) string {
	switch {
	case !i.AcceptedAt.IsZero():
		return InvitationAccepted
	case !i.RevokedAt.IsZero():
		return InvitationRevoked
	case !i.ExpiresAt.After(now):
		return InvitationExpired
	}
	return InvitationPending
}

// InvitationView is the API representation of an invitation (no token).
type InvitationView struct {
	Invitation
	Status     string     `json:"status"`
	AcceptedAt *time.Time `json:"accepted_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func (s *Service) view(i Invitation) InvitationView {
	return InvitationView{Invitation: i, Status: i.Status(s.now()), AcceptedAt: nullTime(i.AcceptedAt), RevokedAt: nullTime(i.RevokedAt)}
}

// CreatedInvitation is returned once, at creation: Token is the only copy of
// the secret and must be shared with the invitee (e.g. inside a link).
type CreatedInvitation struct {
	InvitationView
	Token string `json:"token"`
}

// AcceptInput accepts an invitation. A new user sets Name and Password; an
// existing user (same e-mail) proves ownership with their current Password.
type AcceptInput struct {
	Token    string `json:"token"`
	Name     string `json:"name,omitempty"`
	Password string `json:"password"`
}

const defaultInvitationTTL = 7 * 24 * time.Hour

func (s *Service) invitationTTL() time.Duration {
	if s.cfg.InvitationTTL > 0 {
		return s.cfg.InvitationTTL
	}
	return defaultInvitationTTL
}

// CreateInvitation invites an e-mail address to the actor's organization.
// The role must be assignable by the actor (CanAssign): admins can invite
// members and viewers only, owners anyone. A previous pending invitation for
// the same e-mail in the same organization is revoked.
func (s *Service) CreateInvitation(ctx context.Context, actor Principal, email string, role Role) (*CreatedInvitation, error) {
	am, err := s.authorize(ctx, actor, PermMembersInvite)
	if err != nil {
		return nil, err
	}
	email = NormalizeEmail(email)
	ve := &ValidationError{}
	if m := ValidateEmail(email); m != "" {
		ve.add("email", m)
	}
	if !role.Valid() {
		ve.add("role", "unknown role")
	}
	if err := ve.orNil(); err != nil {
		return nil, err
	}
	if !CanAssign(am.Role, role) {
		s.audit(ctx, actor.OrgID, actor.UserID, "invitation.refused", "", map[string]any{"reason": "role_escalation", "role": role})
		return nil, ErrForbidden
	}
	if u, err := s.store.UserByEmail(ctx, email); err == nil {
		if _, err := s.store.GetMembership(ctx, actor.OrgID, u.ID); err == nil {
			return nil, ErrAlreadyMember
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	plain, hash, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	inv := Invitation{ID: uuid.NewString(), OrgID: actor.OrgID, Email: email, Role: role, TokenHash: hash,
		InvitedBy: actor.UserID, CreatedAt: now, ExpiresAt: now.Add(s.invitationTTL())}
	if err := s.store.CreateInvitation(ctx, inv, now); err != nil {
		return nil, err
	}
	// The e-mail is personal data: the audit keeps its domain only.
	s.audit(ctx, actor.OrgID, actor.UserID, "invitation.created", inv.ID, map[string]any{"role": role, "email_domain": emailDomain(email), "expires_at": inv.ExpiresAt})
	return &CreatedInvitation{InvitationView: s.view(inv), Token: plain}, nil
}

// ListInvitations lists the invitations of the actor's organization, newest first.
func (s *Service) ListInvitations(ctx context.Context, actor Principal) ([]InvitationView, error) {
	if _, err := s.authorize(ctx, actor, PermMembersRead); err != nil {
		return nil, err
	}
	invs, err := s.store.ListInvitations(ctx, actor.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]InvitationView, 0, len(invs))
	for _, i := range invs {
		out = append(out, s.view(i))
	}
	return out, nil
}

// RevokeInvitation cancels a pending invitation of the actor's organization.
// Revoking needs the same power as inviting with that role.
func (s *Service) RevokeInvitation(ctx context.Context, actor Principal, id string) error {
	am, err := s.authorize(ctx, actor, PermMembersInvite)
	if err != nil {
		return err
	}
	inv, err := s.store.GetInvitation(ctx, actor.OrgID, id)
	if err != nil {
		return err
	}
	if !CanAssign(am.Role, inv.Role) {
		return ErrForbidden
	}
	if err := s.store.RevokeInvitation(ctx, actor.OrgID, id, s.now()); err != nil {
		return err
	}
	s.audit(ctx, actor.OrgID, actor.UserID, "invitation.revoked", id, map[string]any{"role": inv.Role})
	return nil
}

// AcceptInvitation joins the invitation's organization and opens a session in
// it. Unknown, revoked, already accepted and expired tokens all fail the same
// way for the caller (invalid or expired token). The inviter must still be a
// member allowed to grant the role, so a demoted or removed inviter's
// invitations stop working.
func (s *Service) AcceptInvitation(ctx context.Context, in AcceptInput, meta ClientMeta) (*Session, error) {
	if in.Token == "" || len(in.Token) > 256 {
		return nil, ErrInvalidToken
	}
	hash := hashToken(in.Token)
	inv, err := s.store.InvitationByTokenHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	switch inv.Status(now) {
	case InvitationExpired:
		s.audit(ctx, inv.OrgID, "anonymous", "invitation.accept_refused", inv.ID, map[string]any{"reason": "expired"})
		return nil, ErrTokenExpired
	case InvitationAccepted, InvitationRevoked:
		s.audit(ctx, inv.OrgID, "anonymous", "invitation.accept_refused", inv.ID, map[string]any{"reason": inv.Status(now)})
		return nil, ErrInvalidToken
	}
	inviter, err := s.store.GetMembership(ctx, inv.OrgID, inv.InvitedBy)
	if err != nil || !CanAssign(inviter.Role, inv.Role) {
		s.audit(ctx, inv.OrgID, "anonymous", "invitation.accept_refused", inv.ID, map[string]any{"reason": "inviter_lost_rights"})
		return nil, ErrInvalidToken
	}

	var newUser *User
	u, err := s.store.UserByEmail(ctx, inv.Email)
	switch {
	case errors.Is(err, ErrNotFound):
		name := strings.TrimSpace(in.Name)
		ve := &ValidationError{}
		if m := ValidateName(name, 1); m != "" {
			ve.add("name", m)
		}
		if m := ValidatePassword(in.Password, inv.Email); m != "" {
			ve.add("password", m)
		}
		if err := ve.orNil(); err != nil {
			return nil, err
		}
		h, err := s.hasher.Hash(in.Password)
		if err != nil {
			return nil, err
		}
		u = User{ID: uuid.NewString(), Email: inv.Email, Name: name, PasswordHash: h, CreatedAt: now}
		newUser = &u
	case err != nil:
		return nil, err
	default:
		// Linking an existing account: the token alone is not enough, the
		// person must also know that account's password (same lockout as login).
		if u.LockedUntil.After(now) {
			return nil, &LockedError{Until: u.LockedUntil}
		}
		ok, _, verr := s.hasher.Verify(in.Password, u.PasswordHash)
		if verr != nil || !ok {
			upd, ferr := s.store.RecordLoginFailure(ctx, u.ID, s.cfg.MaxFailedAttempts, s.cfg.LockDuration, now)
			if ferr == nil && upd.LockedUntil.After(now) {
				return nil, &LockedError{Until: upd.LockedUntil}
			}
			return nil, ErrInvalidCredentials
		}
	}
	m := Membership{OrgID: inv.OrgID, UserID: u.ID, Role: inv.Role, CreatedAt: now}
	if err := s.store.AcceptInvitation(ctx, hash, newUser, m, now); err != nil {
		return nil, err
	}
	s.audit(ctx, inv.OrgID, u.ID, "invitation.accepted", inv.ID, map[string]any{"role": inv.Role, "new_user": newUser != nil, "invited_by": inv.InvitedBy})
	return s.issueSession(ctx, u, m, uuid.NewString(), meta)
}

// audit forwards an event to the configured audit sink (no-op without one).
// Details never carry secrets, tokens or full e-mail addresses.
func (s *Service) audit(ctx context.Context, orgID, actor, action, entityID string, details map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, AuditEvent{OrgID: orgID, Actor: actor, Action: action, Entity: "invitation", EntityID: entityID, Details: details})
	}
}

// AuditEvent is what the auth module reports to the application audit trail.
type AuditEvent struct {
	OrgID    string
	Actor    string
	Action   string
	Entity   string
	EntityID string
	Details  map[string]any
}

func emailDomain(email string) string {
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		return email[i+1:]
	}
	return ""
}
