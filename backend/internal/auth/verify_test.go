package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/mailer"
)

// captureMailer records the messages instead of sending them.
type captureMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
	fail bool
}

func (c *captureMailer) Send(_ context.Context, m mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("smtp down")
	}
	c.sent = append(c.sent, m)
	return nil
}

func (c *captureMailer) last(t *testing.T) mailer.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		t.Fatal("no e-mail was sent")
	}
	return c.sent[len(c.sent)-1]
}

func (c *captureMailer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

// tokenIn extracts the token from the verification link in a message body.
func tokenIn(t *testing.T, m mailer.Message) string {
	t.Helper()
	i := strings.Index(m.Text, "https://app.example.com/verify?token=")
	if i < 0 {
		t.Fatalf("no verification link in %q", m.Text)
	}
	link := strings.Fields(m.Text[i:])[0]
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

func verifyEnv(t *testing.T, mm *captureMailer) *env {
	return newEnv(t, func(c *Config) {
		c.RequireVerification = true
		c.Mailer = mm
		c.AppURL = "https://app.example.com/"
	})
}

func register(e *env, email, locale string) error {
	_, err := e.svc.Register(context.Background(), RegisterInput{Email: email, Password: goodPW, Name: "Ana Rojas", OrgName: "Rojas SA", Locale: locale}, ClientMeta{})
	return err
}

func TestVerificationFlow(t *testing.T) {
	mm := &captureMailer{}
	e := verifyEnv(t, mm)
	ctx := context.Background()

	// Registering creates the account but opens no session, and sends the link.
	err := register(e, "ana@example.com", "es")
	var vr *VerificationRequiredError
	if !errors.As(err, &vr) || !vr.EmailSent || vr.Email != "ana@example.com" {
		t.Fatalf("register = %v, want VerificationRequiredError{sent}", err)
	}
	msg := mm.last(t)
	if msg.To != "ana@example.com" || !strings.Contains(msg.Subject, "Confirma") || msg.HTML == "" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	// A correct password is not enough until the address is verified; a wrong one stays "invalid credentials".
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("login before verifying = %v, want ErrEmailNotVerified", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: "wrong-Pass-123"}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v, want ErrInvalidCredentials (must not reveal the account)", err)
	}

	// The link verifies, signs the user in, and works once.
	tok := tokenIn(t, msg)
	s, err := e.svc.Verify(ctx, tok, ClientMeta{})
	if err != nil || s.AccessToken == "" || s.User.Email != "ana@example.com" {
		t.Fatalf("verify = %v %+v", err, s)
	}
	if _, err := e.svc.Verify(ctx, tok, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second use = %v, want ErrInvalidToken", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatalf("login after verifying: %v", err)
	}
}

func TestVerificationTokenExpiresAndGarbageIsRejected(t *testing.T) {
	mm := &captureMailer{}
	e := verifyEnv(t, mm)
	ctx := context.Background()
	_ = register(e, "ana@example.com", "en")
	tok := tokenIn(t, mm.last(t))
	if !strings.Contains(mm.last(t).Subject, "Confirm") {
		t.Fatalf("english message expected, got %q", mm.last(t).Subject)
	}
	e.clock.Advance(DefaultVerifyTTL + time.Minute)
	if _, err := e.svc.Verify(ctx, tok, ClientMeta{}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired = %v, want ErrTokenExpired", err)
	}
	for _, bad := range []string{"", "nope", strings.Repeat("a", 300)} {
		if _, err := e.svc.Verify(ctx, bad, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("garbage %q = %v, want ErrInvalidToken", bad[:min(len(bad), 8)], err)
		}
	}
}

func TestResendRetiresTheOldLinkAndNeverLeaksExistence(t *testing.T) {
	mm := &captureMailer{}
	e := verifyEnv(t, mm)
	ctx := context.Background()
	_ = register(e, "ana@example.com", "es")
	first := tokenIn(t, mm.last(t))

	if err := e.svc.ResendVerification(ctx, "ana@example.com", "es"); err != nil {
		t.Fatal(err)
	}
	second := tokenIn(t, mm.last(t))
	if first == second {
		t.Fatal("a resend must issue a new token")
	}
	if _, err := e.svc.Verify(ctx, first, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old link after a resend = %v, want ErrInvalidToken", err)
	}
	if _, err := e.svc.Verify(ctx, second, ClientMeta{}); err != nil {
		t.Fatalf("new link: %v", err)
	}

	// Unknown and already-verified addresses: no error, no e-mail.
	before := mm.count()
	for _, addr := range []string{"nobody@example.com", "ana@example.com", "not-an-email"} {
		if err := e.svc.ResendVerification(ctx, addr, "es"); err != nil {
			t.Fatalf("resend %q = %v, want nil", addr, err)
		}
	}
	if mm.count() != before {
		t.Fatal("no e-mail must be sent to unknown or verified addresses")
	}
}

func TestResendIsRateLimitedPerAddress(t *testing.T) {
	mm := &captureMailer{}
	lim := NewMemoryLimiter()
	e := newEnv(t, func(c *Config) {
		c.RequireVerification, c.Mailer, c.AppURL = true, mm, "https://app.example.com"
		c.LoginLimiter = lim
		c.ResendLimit = Limit{Max: 2, Window: time.Hour}
	})
	ctx := context.Background()
	_ = register(e, "ana@example.com", "es")
	for i := 0; i < 2; i++ {
		if err := e.svc.ResendVerification(ctx, "ana@example.com", "es"); err != nil {
			t.Fatalf("resend %d: %v", i, err)
		}
	}
	if err := e.svc.ResendVerification(ctx, "ana@example.com", "es"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third resend = %v, want ErrRateLimited", err)
	}
}

func TestRegisterSurvivesAMailerOutage(t *testing.T) {
	mm := &captureMailer{fail: true}
	e := verifyEnv(t, mm)
	err := register(e, "ana@example.com", "es")
	var vr *VerificationRequiredError
	if !errors.As(err, &vr) || vr.EmailSent {
		t.Fatalf("register = %v, want VerificationRequiredError{not sent}", err)
	}
	// The account exists; once mail works again a resend delivers the link.
	mm.mu.Lock()
	mm.fail = false
	mm.mu.Unlock()
	if err := e.svc.ResendVerification(context.Background(), "ana@example.com", "es"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Verify(context.Background(), tokenIn(t, mm.last(t)), ClientMeta{}); err != nil {
		t.Fatalf("verify after the outage: %v", err)
	}
}

func TestVerificationOffKeepsTheOldBehaviour(t *testing.T) {
	e := newEnv(t) // RequireVerification false
	s := e.register(t, "ana@example.com", "Rojas")
	if s.AccessToken == "" {
		t.Fatal("with verification off, registering must open a session")
	}
	u, _ := e.store.UserByEmail(context.Background(), "ana@example.com")
	if u.VerifiedAt.IsZero() {
		t.Fatal("with verification off, accounts are created verified")
	}
	if err := e.svc.ResendVerification(context.Background(), "ana@example.com", "es"); err != nil {
		t.Fatalf("resend with verification off must be a no-op, got %v", err)
	}
}

func TestVerificationNeedsAMailerAndAppURL(t *testing.T) {
	if _, err := NewService(Config{Store: NewMemoryStore(), Secret: testSecret, PasswordParams: fastParams, RequireVerification: true}); err == nil {
		t.Fatal("RequireVerification without a Mailer/AppURL must be rejected at startup")
	}
}
