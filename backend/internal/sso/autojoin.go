package sso

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/auth"
)

// Auto-join by verified domain (docs/architecture/identity.md section 3).
//
// An organization can let people whose e-mail address is on a domain it has verified join on their own, with a fixed role
// (viewer by default, never owner). It is OFF until an owner or admin turns it on. Three things must all hold:
//
//  1. the domain is verified by that organization (the DNS challenge), so a stranger cannot claim somebody else's domain;
//  2. the person has PROVEN the address: at password sign-up that means opening the verification link (auth does the join
//     when the link is used, never at registration), at SSO it means the provider's email_verified claim;
//  3. the account was not deprovisioned by SCIM (the same Gate that vetoes SSO).

// ErrAutoJoinNeedsDomain: nothing to match on until the organization has verified a domain.
var ErrAutoJoinNeedsDomain = errors.New("sso: auto-join needs at least one verified domain")

// AutoJoinView is the API representation.
type AutoJoinView struct {
	Enabled bool      `json:"enabled"`
	Role    auth.Role `json:"role"`
	// Domains are the verified domains the setting applies to.
	Domains []string `json:"domains"`
}

// GetAutoJoin returns the setting and the domains it covers.
func (s *Service) GetAutoJoin(ctx context.Context, orgID string) (AutoJoinView, error) {
	a, err := s.o.Store.GetAutoJoin(ctx, orgID)
	if err != nil {
		return AutoJoinView{}, err
	}
	ds, err := s.o.Store.ListDomains(ctx, orgID)
	v := AutoJoinView{Enabled: a.Enabled, Role: a.Role, Domains: []string{}}
	if !v.Role.Valid() || v.Role == auth.RoleOwner {
		v.Role = auth.RoleViewer
	}
	for _, d := range ds {
		if d.Verified() {
			v.Domains = append(v.Domains, d.Domain)
		}
	}
	return v, err
}

// SetAutoJoin stores the setting. Turning it on needs a verified domain; the role can be admin, member or viewer.
func (s *Service) SetAutoJoin(ctx context.Context, orgID, actor string, enabled bool, role auth.Role) (AutoJoinView, error) {
	if role == "" {
		role = auth.RoleViewer
	}
	if !role.Valid() || role == auth.RoleOwner {
		return AutoJoinView{}, &ValidationError{Fields: map[string]string{"role": "must be admin, member or viewer"}}
	}
	if enabled {
		if n, _ := s.verifiedCount(ctx, orgID); n == 0 {
			return AutoJoinView{}, ErrAutoJoinNeedsDomain
		}
	}
	a := AutoJoin{OrgID: orgID, Enabled: enabled, Role: role, UpdatedBy: actor, UpdatedAt: s.o.Now().UTC()}
	if err := s.o.Store.PutAutoJoin(ctx, a); err != nil {
		return AutoJoinView{}, err
	}
	s.o.Audit(ctx, orgID, actor, "org.auto_join_changed", map[string]any{"enabled": enabled, "role": string(role)})
	return s.GetAutoJoin(ctx, orgID)
}

// AutoJoinFor implements auth.AutoJoiner: given an address the caller has already proven, which organization (if any) lets
// this person in, and with which role. It answers "no" (ok=false, nil error) for every ordinary case; an error means the
// lookup itself failed, and the caller must then not join anyone.
func (s *Service) AutoJoinFor(ctx context.Context, email string) (string, auth.Role, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	dom := domainOf(email)
	if dom == "" {
		return "", "", false, nil
	}
	org, err := s.o.Store.OrgForVerifiedDomain(ctx, dom)
	if errors.Is(err, ErrNotFound) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	a, err := s.o.Store.GetAutoJoin(ctx, org)
	if err != nil {
		return "", "", false, err
	}
	if !a.Enabled {
		return "", "", false, nil
	}
	role := a.Role
	if !role.Valid() || role == auth.RoleOwner {
		role = auth.RoleViewer
	}
	if s.o.Gate != nil {
		if err := s.o.Gate(ctx, org, email); err != nil {
			return "", "", false, nil // deprovisioned by SCIM: stays out
		}
	}
	return org, role, true, nil
}

// joinRules decides whether a verified SSO identity may create its account and membership, and with which role: the SSO
// configuration's own just-in-time setting wins; otherwise the organization-wide auto-join setting applies.
func (s *Service) joinRules(ctx context.Context, c Config) (bool, auth.Role) {
	if c.JIT {
		return true, c.DefaultRole
	}
	a, err := s.o.Store.GetAutoJoin(ctx, c.OrgID)
	if err == nil && a.Enabled && a.Role.Valid() && a.Role != auth.RoleOwner {
		return true, a.Role
	}
	return false, c.DefaultRole
}
