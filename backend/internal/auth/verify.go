package auth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/mailer"
)

// Email verification.
//
// With Config.RequireVerification a new account gets no session until its owner opens a one-time link sent to the
// address. The token is random, stored hashed, single-use and short-lived. Logging in with a correct password before
// verifying answers ErrEmailNotVerified (a wrong password still answers "invalid credentials", so the endpoint does not
// reveal which addresses are registered). Resending is rate-limited per address and never says whether the address exists.

// ErrEmailNotVerified is returned by Login when the password is right but the address has not been verified.
var ErrEmailNotVerified = errors.New("auth: email not verified")

// VerificationRequiredError is returned by Register when the account was created but needs verifying before it can
// sign in. EmailSent is false when the message could not be delivered (the user can ask for it again).
type VerificationRequiredError struct {
	Email     string
	EmailSent bool
}

func (e *VerificationRequiredError) Error() string { return "auth: email verification required" }

// EmailVerification is the persisted (hashed) form of a verification token.
type EmailVerification struct {
	TokenHash string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// DefaultVerifyTTL is how long a verification link stays valid.
const DefaultVerifyTTL = 24 * time.Hour

// normLocale keeps the e-mail language to the two we write; anything else is Spanish.
func normLocale(l string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "en") {
		return "en"
	}
	return "es"
}

func (s *Service) verifyEnabled() bool { return s.cfg.RequireVerification }

// sendVerification creates a fresh token (invalidating older ones) and e-mails the link.
func (s *Service) sendVerification(ctx context.Context, u User, locale string) error {
	token, hash, err := newOpaqueToken()
	if err != nil {
		return err
	}
	now := s.now()
	ttl := s.cfg.VerifyTTL
	if err := s.store.CreateEmailVerification(ctx, EmailVerification{TokenHash: hash, UserID: u.ID, ExpiresAt: now.Add(ttl), CreatedAt: now}); err != nil {
		return err
	}
	link := strings.TrimRight(s.cfg.AppURL, "/") + "/verify?token=" + url.QueryEscape(token)
	return s.cfg.Mailer.Send(ctx, verificationMessage(normLocale(locale), u, link, ttl))
}

// Verify consumes a verification token, marks the address verified and opens a session (the user lands signed in).
func (s *Service) Verify(ctx context.Context, token string, meta ClientMeta) (*Session, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 256 {
		return nil, ErrInvalidToken
	}
	u, err := s.store.ConsumeEmailVerification(ctx, hashToken(token), s.now())
	if err != nil {
		return nil, err
	}
	m, err := s.pickMembership(ctx, u.ID, "")
	if err != nil {
		return nil, err
	}
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, AuditEvent{OrgID: m.OrgID, Actor: u.ID, Action: "user.email_verified", Entity: "user", EntityID: u.ID})
	}
	return s.issueSession(ctx, u, m, uuid.NewString(), meta)
}

// ResendVerification sends a new link to an unverified account. It is silent about whether the address exists or is
// already verified; the only error a caller sees is the rate limit.
func (s *Service) ResendVerification(ctx context.Context, email, locale string) error {
	if !s.verifyEnabled() {
		return nil
	}
	email = NormalizeEmail(email)
	if ValidateEmail(email) != "" {
		return nil
	}
	if s.cfg.LoginLimiter != nil {
		res, err := s.cfg.LoginLimiter.Allow(ctx, "verify:email:"+hashToken(email), s.cfg.ResendLimit)
		if err != nil {
			return fmt.Errorf("auth: verify limiter: %w", err)
		}
		if !res.Allowed {
			return &RateLimitedError{RetryAfter: res.RetryAfter}
		}
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) || (err == nil && !u.VerifiedAt.IsZero()) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.sendVerification(ctx, u, locale); err != nil {
		// Delivery trouble is the operator's to fix; the caller learns nothing either way.
		s.cfg.Logf("auth: resend verification failed: %v", err)
	}
	return nil
}

// verificationMessage renders the e-mail in the user's language.
func verificationMessage(lang string, u User, link string, ttl time.Duration) mailer.Message {
	hours := int(ttl.Hours())
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = u.Email
	}
	if lang == "en" {
		text := fmt.Sprintf("Hi %s,\n\nConfirm your email to open your AI Workforce OS office:\n\n%s\n\nThe link works once and expires in %d hours. If you did not create this account, ignore this message and nothing will happen.\n\nAI Workforce OS", name, link, hours)
		return mailer.Message{To: u.Email, Subject: "Confirm your email for AI Workforce OS", Text: text, HTML: htmlBody(fmt.Sprintf("Hi %s,", html.EscapeString(name)), "Confirm your email to open your AI Workforce OS office.", "Confirm email", link, fmt.Sprintf("The link works once and expires in %d hours. If you did not create this account, ignore this message and nothing will happen.", hours))}
	}
	text := fmt.Sprintf("Hola %s,\n\nConfirma tu correo para abrir tu oficina en AI Workforce OS:\n\n%s\n\nEl enlace sirve una sola vez y caduca en %d horas. Si no creaste esta cuenta, ignora este mensaje y no pasará nada.\n\nAI Workforce OS", name, link, hours)
	return mailer.Message{To: u.Email, Subject: "Confirma tu correo en AI Workforce OS", Text: text, HTML: htmlBody(fmt.Sprintf("Hola %s,", html.EscapeString(name)), "Confirma tu correo para abrir tu oficina en AI Workforce OS.", "Confirmar correo", link, fmt.Sprintf("El enlace sirve una sola vez y caduca en %d horas. Si no creaste esta cuenta, ignora este mensaje y no pasará nada.", hours))}
}

func htmlBody(greeting, lead, button, link, fine string) string {
	l := html.EscapeString(link)
	return `<!doctype html><html><body style="margin:0;background:#f7f9fb;font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#0a1628">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">` +
		`<table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border:1px solid #e1e7ee;border-radius:12px"><tr><td style="padding:32px">` +
		`<div style="font-weight:600;font-size:15px;margin-bottom:20px">AI Workforce OS</div>` +
		`<p style="margin:0 0 8px;font-size:16px">` + greeting + `</p>` +
		`<p style="margin:0 0 24px;font-size:16px;line-height:1.5;color:#38485c">` + html.EscapeString(lead) + `</p>` +
		`<p style="margin:0 0 24px"><a href="` + l + `" style="display:inline-block;background:#0f766e;color:#ffffff;text-decoration:none;font-weight:600;font-size:15px;padding:12px 22px;border-radius:8px">` + html.EscapeString(button) + `</a></p>` +
		`<p style="margin:0 0 6px;font-size:12px;color:#5c6b7e">` + html.EscapeString(fine) + `</p>` +
		`<p style="margin:0;font-size:12px;color:#5c6b7e;word-break:break-all">` + l + `</p>` +
		`</td></tr></table></td></tr></table></body></html>`
}
