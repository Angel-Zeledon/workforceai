package auth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"aiworkforce/backend/internal/mailer"
)

// Password reset by e-mail.
//
// ForgotPassword always answers the same whether or not the address has an account (so it cannot be used to discover
// accounts), is rate-limited per address, and sends a one-time link that expires in an hour. ResetPassword consumes
// the link, sets the new password under the normal password policy, ends every existing session of the user, clears any
// lockout, and does NOT open a session: the user signs in again (and meets the second factor, if they have one).
// Opening the link proves the address, so it also counts as verification.

const (
	purposeVerify = "verify"
	purposeReset  = "reset"
	// ResetTTL is how long a reset link stays valid.
	ResetTTL = time.Hour
)

// PasswordResetEnabled reports whether this deployment can e-mail a reset link.
func (s *Service) PasswordResetEnabled() bool {
	return s.cfg.Mailer != nil && strings.TrimSpace(s.cfg.AppURL) != ""
}

// ForgotPassword sends a reset link to the account behind email, if there is one. It never reveals whether there is.
func (s *Service) ForgotPassword(ctx context.Context, email, locale string) error {
	if !s.PasswordResetEnabled() {
		return nil
	}
	email = NormalizeEmail(email)
	if ValidateEmail(email) != "" {
		return nil
	}
	if s.cfg.LoginLimiter != nil {
		res, err := s.cfg.LoginLimiter.Allow(ctx, "reset:email:"+hashToken(email), s.cfg.ResendLimit)
		if err != nil {
			return fmt.Errorf("auth: reset limiter: %w", err)
		}
		if !res.Allowed {
			return &RateLimitedError{RetryAfter: res.RetryAfter}
		}
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) || (err == nil && !u.TrialExpiresAt.IsZero()) { // trial accounts have no real address
		return nil
	}
	if err != nil {
		return err
	}
	token, hash, err := newOpaqueToken()
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.store.CreateEmailVerification(ctx, EmailVerification{TokenHash: hash, UserID: u.ID, Purpose: purposeReset, ExpiresAt: now.Add(ResetTTL), CreatedAt: now}); err != nil {
		return err
	}
	link := strings.TrimRight(s.cfg.AppURL, "/") + "/reset?token=" + url.QueryEscape(token)
	if err := s.cfg.Mailer.Send(ctx, resetMessage(normLocale(locale), u, link)); err != nil {
		s.cfg.Logf("auth: password reset e-mail failed: %v", err) // delivery trouble is the operator's, not the caller's
	}
	s.audit2(ctx, u.ID, "user.password_reset_requested")
	return nil
}

// ResetPassword sets a new password using a reset link.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 256 {
		return ErrInvalidToken
	}
	// Check the password against the policy before spending the one-time token, so a typo does not burn the link.
	probe, err := s.store.PeekEmailToken(ctx, hashToken(token), purposeReset, s.now())
	if err != nil {
		return err
	}
	if m := ValidatePassword(newPassword, probe.Email); m != "" {
		ve := &ValidationError{}
		ve.add("password", m)
		return ve
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	u, err := s.store.ConsumeEmailVerification(ctx, hashToken(token), purposeReset, s.now())
	if err != nil {
		return err
	}
	if err := s.store.UpdatePasswordHash(ctx, u.ID, hash); err != nil {
		return err
	}
	now := s.now()
	_ = s.store.ResetLoginFailures(ctx, u.ID)
	if err := s.store.RevokeUserTokens(ctx, u.ID, now); err != nil {
		return err
	}
	s.audit2(ctx, u.ID, "user.password_reset")
	return nil
}

func resetMessage(lang string, u User, link string) mailer.Message {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = u.Email
	}
	if lang == "en" {
		text := fmt.Sprintf("Hi %s,\n\nSomeone asked to reset the password of your AI Workforce OS account. To choose a new one, open:\n\n%s\n\nThe link works once and expires in 1 hour. If it was not you, ignore this message: your password stays the same.\n\nAI Workforce OS", name, link)
		return mailer.Message{To: u.Email, Subject: "Reset your AI Workforce OS password", Text: text, HTML: htmlBody(fmt.Sprintf("Hi %s,", html.EscapeString(name)), "Someone asked to reset the password of your AI Workforce OS account.", "Choose a new password", link, "The link works once and expires in 1 hour. If it was not you, ignore this message: your password stays the same.")}
	}
	text := fmt.Sprintf("Hola %s,\n\nAlguien pidió restablecer la contraseña de tu cuenta de AI Workforce OS. Para elegir una nueva, abre:\n\n%s\n\nEl enlace sirve una sola vez y caduca en 1 hora. Si no fuiste tú, ignora este mensaje: tu contraseña no cambia.\n\nAI Workforce OS", name, link)
	return mailer.Message{To: u.Email, Subject: "Restablece tu contraseña de AI Workforce OS", Text: text, HTML: htmlBody(fmt.Sprintf("Hola %s,", html.EscapeString(name)), "Alguien pidió restablecer la contraseña de tu cuenta de AI Workforce OS.", "Elegir una contraseña nueva", link, "El enlace sirve una sola vez y caduca en 1 hora. Si no fuiste tú, ignora este mensaje: tu contraseña no cambia.")}
}
