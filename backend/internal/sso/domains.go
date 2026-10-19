package sso

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"
)

// Verified domains (docs/architecture/identity.md). An organization proves it controls example.com by publishing
//
//	_aiworkforce-challenge.example.com  TXT  "aiworkforce-verification=<token>"
//
// Only a verified domain can route "sign in with SSO" to the organization or allow accounts to be created on first login.
// A domain can be verified by exactly one organization at a time.

const (
	challengePrefix = "_aiworkforce-challenge."
	challengeValue  = "aiworkforce-verification="
)

var (
	// ErrBadDomain: not a registrable-looking DNS name, or one nobody may claim.
	ErrBadDomain = errors.New("sso: invalid domain")
	// ErrFreeMail: public mailbox providers can never be claimed by an organization.
	ErrFreeMail = errors.New("sso: public mailbox providers cannot be verified")
	// ErrChallengeMissing: the TXT record was not found (yet).
	ErrChallengeMissing = errors.New("sso: the verification record was not found")
	// ErrDomainInUse: the domain is the only one behind an enforced SSO policy.
	ErrDomainInUse = errors.New("sso: this is the last verified domain and SSO is enforced")
)

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// freeMail lists providers whose domains are shared by millions of unrelated people.
var freeMail = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"yahoo.com": true, "yahoo.es": true, "ymail.com": true, "icloud.com": true, "me.com": true, "mac.com": true,
	"aol.com": true, "proton.me": true, "protonmail.com": true, "pm.me": true, "gmx.com": true, "gmx.net": true,
	"mail.com": true, "zoho.com": true, "yandex.com": true, "yandex.ru": true, "qq.com": true, "163.com": true,
	"126.com": true, "hotmail.es": true, "outlook.es": true, "live.com.mx": true, "hotmail.com.mx": true,
	"uol.com.br": true, "bol.com.br": true, "terra.com.br": true, "globo.com": true, "fastmail.com": true,
	"tutanota.com": true, "hey.com": true,
}

// NormalizeDomain lower-cases and validates a domain typed by an administrator.
func NormalizeDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(in))
	d = strings.TrimSuffix(d, ".")
	if i := strings.LastIndexByte(d, '@'); i >= 0 { // accept "someone@example.com" pasted by mistake
		d = d[i+1:]
	}
	if d == "" || len(d) > 253 || net.ParseIP(d) != nil {
		return "", ErrBadDomain
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", ErrBadDomain
	}
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return "", ErrBadDomain
		}
	}
	if tld := labels[len(labels)-1]; len(tld) < 2 || strings.ContainsAny(tld, "0123456789") && !strings.HasPrefix(tld, "xn--") {
		return "", ErrBadDomain
	}
	// A bare public suffix (co.uk, com.br) is not an organization's domain. This is a conservative heuristic, not the
	// Public Suffix List: two-label names whose first label is a generic second-level label are refused.
	if len(labels) == 2 && len(labels[1]) == 2 && secondLevel[labels[0]] {
		return "", ErrBadDomain
	}
	if freeMail[d] {
		return "", ErrFreeMail
	}
	switch labels[len(labels)-1] {
	case "invalid", "local", "localhost", "test", "example", "internal", "lan":
		return "", ErrBadDomain
	}
	return d, nil
}

var secondLevel = map[string]bool{"co": true, "com": true, "org": true, "net": true, "gov": true, "edu": true, "ac": true, "or": true, "ne": true, "go": true}

// ChallengeRecord is what the administrator must publish.
type ChallengeRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// DomainView is the API representation of a Domain.
type DomainView struct {
	Domain
	Challenge ChallengeRecord `json:"challenge"`
	Verified  bool            `json:"verified"`
}

func viewOfDomain(d Domain) DomainView {
	return DomainView{Domain: d, Verified: d.Verified(),
		Challenge: ChallengeRecord{Name: challengePrefix + d.Domain, Type: "TXT", Value: challengeValue + d.Token}}
}

// ListDomains returns the organization's domains with the record to publish for each.
func (s *Service) ListDomains(ctx context.Context, orgID string) ([]DomainView, error) {
	ds, err := s.o.Store.ListDomains(ctx, orgID)
	out := make([]DomainView, 0, len(ds))
	for _, d := range ds {
		out = append(out, viewOfDomain(d))
	}
	return out, err
}

// AddDomain registers a domain and its challenge token (idempotent: adding again returns the same token).
func (s *Service) AddDomain(ctx context.Context, orgID, actor, domain string) (DomainView, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return DomainView{}, err
	}
	if _, err := s.o.Store.GetDomain(ctx, orgID, d); err == nil {
		got, _ := s.o.Store.GetDomain(ctx, orgID, d)
		return viewOfDomain(got), nil
	}
	nd := Domain{OrgID: orgID, Domain: d, Token: randToken(24), CreatedAt: s.o.Now().UTC()}
	if err := s.o.Store.AddDomain(ctx, nd); err != nil {
		return DomainView{}, err
	}
	got, err := s.o.Store.GetDomain(ctx, orgID, d)
	if err != nil {
		return DomainView{}, err
	}
	s.o.Audit(ctx, orgID, actor, "domain.added", map[string]any{"domain": d})
	return viewOfDomain(got), nil
}

// VerifyDomain looks up the TXT record and, when it carries this organization's token, marks the domain verified.
func (s *Service) VerifyDomain(ctx context.Context, orgID, actor, domain string) (DomainView, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return DomainView{}, err
	}
	rec, err := s.o.Store.GetDomain(ctx, orgID, d)
	if err != nil {
		return DomainView{}, err
	}
	if rec.Verified() {
		return viewOfDomain(rec), nil
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	txts, lerr := s.o.Resolver.LookupTXT(cctx, challengePrefix+d)
	_ = s.o.Store.TouchDomainCheck(ctx, orgID, d, s.o.Now().UTC())
	want := challengeValue + rec.Token
	found := false
	if lerr == nil {
		for _, t := range txts {
			if strings.TrimSpace(strings.Trim(t, `"`)) == want {
				found = true
				break
			}
		}
	}
	if !found {
		return DomainView{}, ErrChallengeMissing
	}
	if err := s.o.Store.MarkVerified(ctx, orgID, d, s.o.Now().UTC()); err != nil {
		return DomainView{}, err
	}
	s.o.Audit(ctx, orgID, actor, "domain.verified", map[string]any{"domain": d})
	rec, err = s.o.Store.GetDomain(ctx, orgID, d)
	return viewOfDomain(rec), err
}

// RemoveDomain forgets a domain. The last verified domain cannot go while SSO is enforced.
func (s *Service) RemoveDomain(ctx context.Context, orgID, actor, domain string) error {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return err
	}
	rec, err := s.o.Store.GetDomain(ctx, orgID, d)
	if err != nil {
		return err
	}
	if rec.Verified() {
		if p, _ := s.o.Store.GetPolicy(ctx, orgID); p.EnforceSSO {
			if n, _ := s.verifiedCount(ctx, orgID); n <= 1 {
				return ErrDomainInUse
			}
		}
	}
	if err := s.o.Store.DeleteDomain(ctx, orgID, d); err != nil {
		return err
	}
	s.o.Audit(ctx, orgID, actor, "domain.removed", map[string]any{"domain": d})
	return nil
}

type defaultResolver struct{}

func (defaultResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}
