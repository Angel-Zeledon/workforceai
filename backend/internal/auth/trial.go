package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Trial accounts: "try it without signing up". The server mints a throwaway account (its own organization, so it is as
// isolated as any other), returns the generated user and password ONCE, and the account stops working after TrialTTL.
// The password lives nowhere else: only its hash is stored. Creation is rate-limited per client address and globally.

var (
	// ErrTrialExpired is returned by Login for a trial account whose time is up.
	ErrTrialExpired = errors.New("auth: trial account expired")
	// ErrTrialDisabled is returned when trial accounts are not enabled on this deployment.
	ErrTrialDisabled = errors.New("auth: trial accounts are disabled")
)

// DefaultTrialTTL is how long a trial account works.
const DefaultTrialTTL = 24 * time.Hour

// TrialAccount is what the visitor is shown. The password is never retrievable afterwards.
type TrialAccount struct {
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	ExpiresAt time.Time `json:"expires_at"`
}

func trialExpired(u User, now time.Time) bool {
	return !u.TrialExpiresAt.IsZero() && !u.TrialExpiresAt.After(now)
}

// trialAlphabet has no 0/O/1/I/L: the password is read off a screen and typed by hand.
const trialAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// trialPassword returns a 12-character password in three groups, e.g. "K7QD-3MWX-H9TR" (about 59 bits).
func trialPassword() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(trialAlphabet[int(v)%len(trialAlphabet)]) // 256 % 31 != 0: the bias is far below what matters at 59 bits
	}
	return sb.String(), nil
}

// CreateTrial mints a trial account with its own organization.
func (s *Service) CreateTrial(ctx context.Context, locale string) (*TrialAccount, error) {
	if !s.cfg.TrialEnabled {
		return nil, ErrTrialDisabled
	}
	if s.cfg.LoginLimiter != nil {
		res, err := s.cfg.LoginLimiter.Allow(ctx, "trial:global", Limit{Max: s.cfg.TrialGlobalPerDay, Window: 24 * time.Hour})
		if err != nil {
			return nil, err
		}
		if !res.Allowed {
			return nil, &RateLimitedError{RetryAfter: res.RetryAfter}
		}
	}
	pw, err := trialPassword()
	if err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(pw)
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(s.cfg.TrialTTL)
	orgName, userName := "Oficina de prueba", "Invitado"
	if normLocale(locale) == "en" {
		orgName, userName = "Trial office", "Guest"
	}
	for attempt := 0; ; attempt++ {
		email := "prueba-" + randomHex(4) + "@" + s.cfg.TrialEmailDomain
		u := User{ID: uuid.NewString(), Email: email, Name: userName, PasswordHash: hash, CreatedAt: now, VerifiedAt: now, TrialExpiresAt: exp}
		o := Org{ID: uuid.NewString(), Name: orgName, Slug: slugify(orgName) + "-" + randomHex(3), BudgetUSD: s.cfg.DefaultOrgBudgetUSD}
		m := Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: now}
		err = s.store.CreateAccount(ctx, u, o, m)
		if (errors.Is(err, ErrSlugTaken) || errors.Is(err, ErrEmailTaken)) && attempt < 3 {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.cfg.Audit != nil {
			s.cfg.Audit(ctx, AuditEvent{OrgID: o.ID, Actor: u.ID, Action: "trial.created", Entity: "user", EntityID: u.ID, Details: map[string]any{"expires_at": exp}})
		}
		return &TrialAccount{Email: email, Password: pw, ExpiresAt: exp}, nil
	}
}
