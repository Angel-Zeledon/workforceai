package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/mailer"
)

func resetEnv(t *testing.T, mm *captureMailer, mut ...func(*Config)) *env {
	return newEnv(t, append([]func(*Config){func(c *Config) { c.Mailer = mm; c.AppURL = "https://app.example.com" }}, mut...)...)
}

func resetToken(t *testing.T, m mailer.Message) string {
	t.Helper()
	i := strings.Index(m.Text, "https://app.example.com/reset?token=")
	if i < 0 {
		t.Fatalf("no reset link in %q", m.Text)
	}
	u, err := url.Parse(strings.Fields(m.Text[i:])[0])
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

func TestPasswordResetFlow(t *testing.T) {
	mm := &captureMailer{}
	e := resetEnv(t, mm)
	ctx := context.Background()
	old := e.register(t, "ana@example.com", "Rojas")
	if !e.svc.PasswordResetEnabled() {
		t.Fatal("reset must be available with a mailer and an app URL")
	}
	if err := e.svc.ForgotPassword(ctx, "ana@example.com", "es"); err != nil {
		t.Fatal(err)
	}
	msg := mm.last(t)
	if msg.To != "ana@example.com" || !strings.Contains(msg.Subject, "Restablece") {
		t.Fatalf("unexpected message %+v", msg)
	}
	tok := resetToken(t, msg)

	// A weak password is refused WITHOUT spending the link.
	if err := e.svc.ResetPassword(ctx, tok, "short"); !errors.Is(err, ErrValidation) {
		t.Fatalf("weak password = %v, want ErrValidation", err)
	}
	const newPW = "Another-Horse-77"
	if err := e.svc.ResetPassword(ctx, tok, newPW); err != nil {
		t.Fatalf("reset with the same link after a typo: %v", err)
	}
	// The link is single-use; the old password and the old session are gone; the new password works.
	if err := e.svc.ResetPassword(ctx, tok, "Yet-Another-Pass-1"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second use = %v, want ErrInvalidToken", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := e.svc.Refresh(ctx, old.RefreshToken, ClientMeta{}); err == nil {
		t.Fatal("sessions opened before the reset must end")
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: newPW}, ClientMeta{}); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
}

func TestPasswordResetNeverRevealsAccountsAndExpires(t *testing.T) {
	mm := &captureMailer{}
	e := resetEnv(t, mm)
	ctx := context.Background()
	e.register(t, "ana@example.com", "Rojas")
	before := mm.count()
	for _, addr := range []string{"nobody@example.com", "not-an-email", ""} {
		if err := e.svc.ForgotPassword(ctx, addr, "es"); err != nil {
			t.Fatalf("%q = %v, want nil (same answer for every address)", addr, err)
		}
	}
	if mm.count() != before {
		t.Fatal("no e-mail for unknown addresses")
	}
	_ = e.svc.ForgotPassword(ctx, "ana@example.com", "en")
	tok := resetToken(t, mm.last(t))
	if !strings.Contains(mm.last(t).Subject, "Reset") {
		t.Fatal("english message expected")
	}
	e.clock.Advance(ResetTTL + time.Minute)
	if err := e.svc.ResetPassword(ctx, tok, "Another-Horse-77"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired link = %v, want ErrTokenExpired", err)
	}
}

func TestResetLinkAndVerificationLinkAreNotInterchangeable(t *testing.T) {
	mm := &captureMailer{}
	e := newEnv(t, func(c *Config) { c.RequireVerification, c.Mailer, c.AppURL = true, mm, "https://app.example.com" })
	ctx := context.Background()
	_ = register(e, "ana@example.com", "es") // sends a verification link
	vtok := tokenIn(t, mm.last(t))
	if err := e.svc.ResetPassword(ctx, vtok, "Another-Horse-77"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a verification link must not reset a password: %v", err)
	}
	if _, err := e.svc.Verify(ctx, vtok, ClientMeta{}); err != nil {
		t.Fatalf("the verification link still works: %v", err)
	}
	_ = e.svc.ForgotPassword(ctx, "ana@example.com", "es")
	rtok := resetToken(t, mm.last(t))
	if _, err := e.svc.Verify(ctx, rtok, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a reset link must not verify an address/open a session: %v", err)
	}
}

func TestPasswordResetOffWithoutMail(t *testing.T) {
	e := newEnv(t)
	e.register(t, "ana@example.com", "Rojas")
	if e.svc.PasswordResetEnabled() {
		t.Fatal("no mailer: reset must be off")
	}
	if err := e.svc.ForgotPassword(context.Background(), "ana@example.com", "es"); err != nil {
		t.Fatalf("must be a silent no-op, got %v", err)
	}
}
