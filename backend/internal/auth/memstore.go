package auth

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-memory Store for tests and local development.
type MemoryStore struct {
	mu          sync.Mutex
	users       map[string]User   // by id
	emails      map[string]string // email -> id
	orgs        map[string]Org
	slugs       map[string]string
	memberships map[[2]string]Membership // {org, user}
	tokens      map[string]*RefreshToken // by hash
	invitations map[string]*Invitation   // by id
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:       map[string]User{},
		emails:      map[string]string{},
		orgs:        map[string]Org{},
		slugs:       map[string]string{},
		memberships: map[[2]string]Membership{},
		tokens:      map[string]*RefreshToken{},
		invitations: map[string]*Invitation{},
	}
}

func (s *MemoryStore) CreateAccount(_ context.Context, u User, o Org, m Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.emails[u.Email]; ok {
		return ErrEmailTaken
	}
	if _, ok := s.slugs[o.Slug]; ok {
		return ErrSlugTaken
	}
	s.users[u.ID] = u
	s.emails[u.Email] = u.ID
	s.orgs[o.ID] = o
	s.slugs[o.Slug] = o.ID
	s.memberships[[2]string{m.OrgID, m.UserID}] = m
	return nil
}

func (s *MemoryStore) UserByEmail(_ context.Context, email string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emails[email]
	if !ok {
		return User{}, ErrNotFound
	}
	return s.users[id], nil
}

func (s *MemoryStore) UserByID(_ context.Context, id string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *MemoryStore) MembershipsOf(_ context.Context, userID string) ([]Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Membership
	for _, m := range s.memberships {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].OrgID < out[j].OrgID
	})
	return out, nil
}

func (s *MemoryStore) GetMembership(_ context.Context, orgID, userID string) (Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.memberships[[2]string{orgID, userID}]
	if !ok {
		return Membership{}, ErrNotFound
	}
	return m, nil
}

func (s *MemoryStore) ListMembers(_ context.Context, orgID string) ([]Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Member
	for k, m := range s.memberships {
		if k[0] != orgID {
			continue
		}
		u := s.users[m.UserID]
		out = append(out, Member{UserID: u.ID, Email: u.Email, Name: u.Name, Role: m.Role, CreatedAt: m.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (s *MemoryStore) AddMembership(_ context.Context, m Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{m.OrgID, m.UserID}
	if _, ok := s.memberships[k]; ok {
		return ErrAlreadyMember
	}
	if _, ok := s.users[m.UserID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.orgs[m.OrgID]; !ok {
		return ErrNotFound
	}
	s.memberships[k] = m
	return nil
}

func (s *MemoryStore) owners(orgID string) int {
	n := 0
	for k, m := range s.memberships {
		if k[0] == orgID && m.Role == RoleOwner {
			n++
		}
	}
	return n
}

func (s *MemoryStore) SetMemberRole(_ context.Context, orgID, userID string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{orgID, userID}
	m, ok := s.memberships[k]
	if !ok {
		return ErrNotFound
	}
	if m.Role == RoleOwner && role != RoleOwner && s.owners(orgID) <= 1 {
		return ErrLastOwner
	}
	m.Role = role
	s.memberships[k] = m
	return nil
}

func (s *MemoryStore) RemoveMember(_ context.Context, orgID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{orgID, userID}
	m, ok := s.memberships[k]
	if !ok {
		return ErrNotFound
	}
	if m.Role == RoleOwner && s.owners(orgID) <= 1 {
		return ErrLastOwner
	}
	delete(s.memberships, k)
	return nil
}

func (s *MemoryStore) RecordLoginFailure(_ context.Context, userID string, max int, lockFor time.Duration, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return User{}, ErrNotFound
	}
	if !u.LockedUntil.IsZero() && !u.LockedUntil.After(now) {
		u.FailedAttempts, u.LockedUntil = 0, time.Time{}
	}
	u.FailedAttempts++
	if u.FailedAttempts >= max {
		u.LockedUntil = now.Add(lockFor)
	}
	s.users[userID] = u
	return u, nil
}

func (s *MemoryStore) ResetLoginFailures(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.FailedAttempts, u.LockedUntil = 0, time.Time{}
	s.users[userID] = u
	return nil
}

func (s *MemoryStore) UpdatePasswordHash(_ context.Context, userID, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.PasswordHash = hash
	s.users[userID] = u
	return nil
}

func (s *MemoryStore) SaveRefreshToken(_ context.Context, t RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := t
	s.tokens[t.TokenHash] = &c
	return nil
}

func (s *MemoryStore) revokeFamily(family string, now time.Time) {
	for _, t := range s.tokens {
		if t.FamilyID == family && t.RevokedAt.IsZero() {
			t.RevokedAt = now
		}
	}
}

func (s *MemoryStore) RotateRefreshToken(_ context.Context, oldHash string, next RefreshToken, now time.Time) (RefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.tokens[oldHash]
	if !ok {
		return RefreshToken{}, ErrInvalidToken
	}
	if !old.RevokedAt.IsZero() {
		s.revokeFamily(old.FamilyID, now)
		return RefreshToken{}, ErrTokenReuse
	}
	if !old.ExpiresAt.After(now) {
		return RefreshToken{}, ErrTokenExpired
	}
	old.RevokedAt = now
	old.ReplacedBy = next.ID
	c := next
	c.UserID, c.OrgID, c.FamilyID = old.UserID, old.OrgID, old.FamilyID
	s.tokens[next.TokenHash] = &c
	return *old, nil
}

func (s *MemoryStore) RevokeFamilyOf(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tokens[tokenHash]; ok {
		s.revokeFamily(t.FamilyID, now)
	}
	return nil
}

func (s *MemoryStore) RevokeUserTokens(_ context.Context, userID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		if t.UserID == userID && t.RevokedAt.IsZero() {
			t.RevokedAt = now
		}
	}
	return nil
}

func (s *MemoryStore) RevokeOrgUserTokens(_ context.Context, orgID, userID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		if t.UserID == userID && t.OrgID == orgID && t.RevokedAt.IsZero() {
			t.RevokedAt = now
		}
	}
	return nil
}

// ActiveTokens counts non-revoked refresh tokens of a user (test helper).
func (s *MemoryStore) ActiveTokens(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tokens {
		if t.UserID == userID && t.RevokedAt.IsZero() {
			n++
		}
	}
	return n
}

func (s *MemoryStore) CreateInvitation(_ context.Context, inv Invitation, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[inv.OrgID]; !ok {
		return ErrNotFound
	}
	for _, o := range s.invitations {
		if o.OrgID == inv.OrgID && o.Email == inv.Email && o.Status(now) == InvitationPending {
			o.RevokedAt = now
		}
	}
	c := inv
	s.invitations[inv.ID] = &c
	return nil
}

func (s *MemoryStore) ListInvitations(_ context.Context, orgID string) ([]Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Invitation
	for _, i := range s.invitations {
		if i.OrgID == orgID {
			out = append(out, *i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) GetInvitation(_ context.Context, orgID, id string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.invitations[id]
	if !ok || i.OrgID != orgID {
		return Invitation{}, ErrNotFound
	}
	return *i, nil
}

func (s *MemoryStore) RevokeInvitation(_ context.Context, orgID, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.invitations[id]
	if !ok || i.OrgID != orgID || i.Status(now) != InvitationPending {
		return ErrNotFound
	}
	i.RevokedAt = now
	return nil
}

func (s *MemoryStore) InvitationByTokenHash(_ context.Context, tokenHash string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range s.invitations {
		if i.TokenHash == tokenHash {
			return *i, nil
		}
	}
	return Invitation{}, ErrNotFound
}

func (s *MemoryStore) AcceptInvitation(_ context.Context, tokenHash string, newUser *User, m Membership, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var inv *Invitation
	for _, i := range s.invitations {
		if i.TokenHash == tokenHash {
			inv = i
		}
	}
	if inv == nil {
		return ErrInvalidToken
	}
	switch inv.Status(now) {
	case InvitationExpired:
		return ErrTokenExpired
	case InvitationAccepted, InvitationRevoked:
		return ErrInvalidToken
	}
	if newUser != nil {
		if _, ok := s.emails[newUser.Email]; ok {
			return ErrEmailTaken
		}
	}
	k := [2]string{m.OrgID, m.UserID}
	if _, ok := s.memberships[k]; ok {
		return ErrAlreadyMember
	}
	if newUser != nil {
		s.users[newUser.ID] = *newUser
		s.emails[newUser.Email] = newUser.ID
	}
	s.memberships[k] = m
	inv.AcceptedAt, inv.AcceptedBy = now, m.UserID
	return nil
}
