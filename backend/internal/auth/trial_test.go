package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func trialEnv(t *testing.T, mut ...func(*Config)) *env {
	return newEnv(t, append([]func(*Config){func(c *Config) { c.TrialEnabled = true }}, mut...)...)
}

func TestTrialAccountWorksThenExpires(t *testing.T) {
	e := trialEnv(t)
	ctx := context.Background()
	acct, err := e.svc.CreateTrial(ctx, "es")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}$`).MatchString(acct.Password) {
		t.Fatalf("password %q is not in the readable XXXX-XXXX-XXXX form", acct.Password)
	}
	if !strings.HasPrefix(acct.Email, "prueba-") || !strings.HasSuffix(acct.Email, "@prueba.invalid") {
		t.Fatalf("unexpected address %q", acct.Email)
	}
	if got := acct.ExpiresAt.Sub(e.clock.Now()); got != DefaultTrialTTL {
		t.Fatalf("expires in %v, want %v", got, DefaultTrialTTL)
	}

	// The credentials sign in, in an organization of their own, and the user is marked as a trial.
	s, err := e.svc.Login(ctx, LoginInput{Email: acct.Email, Password: acct.Password}, ClientMeta{})
	if err != nil {
		t.Fatalf("login with the shown credentials: %v", err)
	}
	if s.User.TrialExpiresAt == nil || !s.User.TrialExpiresAt.Equal(acct.ExpiresAt) || s.Role != RoleOwner {
		t.Fatalf("session = %+v", s)
	}
	// No access token outlives the trial.
	if s.ExpiresAt.After(acct.ExpiresAt) {
		t.Fatalf("access token expires %v, after the trial %v", s.ExpiresAt, acct.ExpiresAt)
	}
	// Late in the trial the access token is clamped to the trial's end.
	e.clock.Advance(DefaultTrialTTL - 5*time.Minute)
	late, err := e.svc.Login(ctx, LoginInput{Email: acct.Email, Password: acct.Password}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !late.ExpiresAt.Equal(acct.ExpiresAt) {
		t.Fatalf("late token expires %v, want exactly the trial end %v", late.ExpiresAt, acct.ExpiresAt)
	}

	// Time is up: no new session, no refresh.
	e.clock.Advance(10 * time.Minute)
	if _, err := e.svc.Login(ctx, LoginInput{Email: acct.Email, Password: acct.Password}, ClientMeta{}); !errors.Is(err, ErrTrialExpired) {
		t.Fatalf("login after expiry = %v, want ErrTrialExpired", err)
	}
	if _, err := e.svc.Refresh(ctx, late.RefreshToken, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh after expiry = %v, want ErrInvalidToken", err)
	}
	// A wrong password after expiry still looks like any wrong password.
	if _, err := e.svc.Login(ctx, LoginInput{Email: acct.Email, Password: "WRONG-PASS-12"}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v, want ErrInvalidCredentials", err)
	}
}

func TestTrialsAreIndependentAccounts(t *testing.T) {
	e := trialEnv(t)
	ctx := context.Background()
	a, _ := e.svc.CreateTrial(ctx, "es")
	b, _ := e.svc.CreateTrial(ctx, "en")
	if a.Email == b.Email || a.Password == b.Password {
		t.Fatal("every trial gets its own address and password")
	}
	sa, _ := e.svc.Login(ctx, LoginInput{Email: a.Email, Password: a.Password}, ClientMeta{})
	sb, _ := e.svc.Login(ctx, LoginInput{Email: b.Email, Password: b.Password}, ClientMeta{})
	if sa.OrgID == sb.OrgID {
		t.Fatal("every trial has its own organization")
	}
}

func TestTrialDisabledAndRateLimited(t *testing.T) {
	off := newEnv(t)
	if _, err := off.svc.CreateTrial(context.Background(), "es"); !errors.Is(err, ErrTrialDisabled) {
		t.Fatalf("disabled = %v, want ErrTrialDisabled", err)
	}
	e := trialEnv(t, func(c *Config) {
		c.LoginLimiter = NewMemoryLimiter()
		c.TrialGlobalPerDay = 2
	})
	for i := 0; i < 2; i++ {
		if _, err := e.svc.CreateTrial(context.Background(), "es"); err != nil {
			t.Fatalf("trial %d: %v", i, err)
		}
	}
	if _, err := e.svc.CreateTrial(context.Background(), "es"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over the daily cap = %v, want ErrRateLimited", err)
	}
}

func TestTrialSkipsEmailVerification(t *testing.T) {
	mm := &captureMailer{}
	e := verifyEnv(t, mm)
	e.svc.cfg.TrialEnabled = true
	acct, err := e.svc.CreateTrial(context.Background(), "es")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(context.Background(), LoginInput{Email: acct.Email, Password: acct.Password}, ClientMeta{}); err != nil {
		t.Fatalf("a trial needs no e-mail verification: %v", err)
	}
	if mm.count() != 0 {
		t.Fatal("no e-mail is sent for a trial account")
	}
}
