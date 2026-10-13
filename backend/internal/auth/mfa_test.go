package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, HMAC-SHA1, secret "12345678901234567890": the 6-digit code is the last six digits of the 8-digit
// reference value.
func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpAt(secret, ts/30); got != want {
			t.Errorf("T=%d: got %s want %s", ts, got, want)
		}
	}
	// The skew window accepts one step either side, and nothing further.
	now := time.Unix(1111111109, 0)
	if _, ok := matchTOTP(secret, "081804", now); !ok {
		t.Error("current step must match")
	}
	if _, ok := matchTOTP(secret, totpAt(secret, stepOf(now)-1), now); !ok {
		t.Error("previous step must match (clock drift)")
	}
	if _, ok := matchTOTP(secret, totpAt(secret, stepOf(now)+3), now); ok {
		t.Error("a code three steps ahead must not match")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok := matchTOTP(secret, bad, now); ok && bad != "" {
			t.Errorf("%q must not match", bad)
		}
	}
}

// enrol registers an account, starts and finishes the TOTP setup, and returns the secret and the recovery codes.
func enrol(t *testing.T, e *env, email string) (secret []byte, recovery []string, userID string) {
	t.Helper()
	ctx := context.Background()
	s := e.register(t, email, "Rojas")
	userID = s.User.ID
	info, err := e.svc.MFASetup(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.URI, "otpauth://totp/") || !strings.Contains(info.URI, "secret="+info.Secret) {
		t.Fatalf("bad otpauth uri %q", info.URI)
	}
	secret, err = b32.DecodeString(info.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MFAEnable(ctx, userID, "000000"); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("enable with a wrong code = %v, want ErrMFAInvalidCode", err)
	}
	recovery, err = e.svc.MFAEnable(ctx, userID, totpAt(secret, stepOf(e.clock.Now())))
	if err != nil || len(recovery) != recoveryCodes {
		t.Fatalf("enable = %v %v", recovery, err)
	}
	return secret, recovery, userID
}

func TestMFALoginNeedsTheSecondFactor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	secret, _, uid := enrol(t, e, "ana@example.com")

	// Password alone no longer opens a session.
	_, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	if !errors.As(err, &mr) || mr.Token == "" {
		t.Fatalf("login = %v, want MFARequiredError", err)
	}
	// The challenge is not an access token.
	if _, aerr := e.svc.Authenticate(ctx, mr.Token); aerr == nil {
		t.Fatal("the MFA challenge must not authenticate as an access token")
	}
	// A wrong code is refused; the right one (next time step, so it is not the one used to enable) opens the session.
	if _, err := e.svc.VerifyMFA(ctx, mr.Token, "123456", ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("wrong code = %v", err)
	}
	e.clock.Advance(30 * time.Second)
	code := totpAt(secret, stepOf(e.clock.Now()))
	s, err := e.svc.VerifyMFA(ctx, mr.Token, code, ClientMeta{})
	if err != nil || s.AccessToken == "" || s.User.ID != uid {
		t.Fatalf("verify = %v %+v", err, s)
	}
	// The same code cannot be replayed.
	if _, err := e.svc.VerifyMFA(ctx, mr.Token, code, ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("replay = %v, want ErrMFAInvalidCode", err)
	}
	// /auth/me tells the UI.
	p, _ := e.svc.Authenticate(ctx, s.AccessToken)
	if me, _ := e.svc.Me(ctx, p); !me.MFAEnabled {
		t.Fatal("Me must report mfa_enabled")
	}
}

func TestMFAChallengeExpiresAndCannotBeForged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	secret, _, _ := enrol(t, e, "ana@example.com")
	_, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	_ = errors.As(err, &mr)
	// Tampering with the payload breaks the MAC.
	forged := "x" + mr.Token
	if _, err := e.svc.VerifyMFA(ctx, forged, "123456", ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged challenge = %v", err)
	}
	e.clock.Advance(mfaChallengeTTL + time.Second)
	if _, err := e.svc.VerifyMFA(ctx, mr.Token, totpAt(secret, stepOf(e.clock.Now())), ClientMeta{}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired challenge = %v, want ErrTokenExpired", err)
	}
}

func TestMFARecoveryCodesWorkOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, recovery, _ := enrol(t, e, "ana@example.com")
	login := func() string {
		_, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{})
		var mr *MFARequiredError
		if !errors.As(err, &mr) {
			t.Fatalf("login = %v", err)
		}
		return mr.Token
	}
	if _, err := e.svc.VerifyMFA(ctx, login(), recovery[0], ClientMeta{}); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if _, err := e.svc.VerifyMFA(ctx, login(), recovery[0], ClientMeta{}); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("a used recovery code = %v, want ErrMFAInvalidCode", err)
	}
	// Case and spacing are forgiven; a different code still works.
	if _, err := e.svc.VerifyMFA(ctx, login(), " "+strings.ToUpper(recovery[1])+" ", ClientMeta{}); err != nil {
		t.Fatalf("recovery code typed in capitals: %v", err)
	}
}

func TestMFAWrongCodesLockTheAccount(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxFailedAttempts = 3 })
	ctx := context.Background()
	enrol(t, e, "ana@example.com")
	_, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	_ = errors.As(err, &mr)
	var last error
	for i := 0; i < 3; i++ {
		_, last = e.svc.VerifyMFA(ctx, mr.Token, "000000", ClientMeta{})
	}
	if !errors.Is(last, ErrAccountLocked) {
		t.Fatalf("after 3 wrong codes = %v, want ErrAccountLocked", last)
	}
}

func TestMFADisableNeedsPasswordAndCode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	secret, _, uid := enrol(t, e, "ana@example.com")
	e.clock.Advance(30 * time.Second)
	code := totpAt(secret, stepOf(e.clock.Now()))
	if err := e.svc.MFADisable(ctx, uid, "wrong-Pass-123", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := e.svc.MFADisable(ctx, uid, goodPW, "000000"); !errors.Is(err, ErrMFAInvalidCode) {
		t.Fatalf("wrong code = %v", err)
	}
	if err := e.svc.MFADisable(ctx, uid, goodPW, code); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "ana@example.com", Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatalf("login after disabling: %v", err)
	}
	if _, err := e.svc.MFASetup(ctx, uid); err != nil {
		t.Fatalf("it can be enrolled again: %v", err)
	}
}

func TestMFASecretIsEncryptedAtRestAndBoundToTheUser(t *testing.T) {
	e := newEnv(t)
	secret, _, uid := enrol(t, e, "ana@example.com")
	rec, _ := e.store.GetMFA(context.Background(), uid)
	if strings.Contains(rec.SecretEnc, encodeSecret(secret)) || rec.SecretEnc == "" {
		t.Fatal("the stored secret must be ciphertext")
	}
	// Moving the ciphertext to another user must fail to decrypt (the user id is authenticated data).
	if _, err := e.svc.openSecret("someone-else", rec.SecretEnc); err == nil {
		t.Fatal("a secret must not decrypt for another user id")
	}
	if got, err := e.svc.openSecret(uid, rec.SecretEnc); err != nil || string(got) != string(secret) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestInvitedExistingUserStillMeetsTheSecondFactor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.register(t, "owner@example.com", "Acme")
	enrol(t, e, "ana@example.com")
	p := e.principal(t, owner)
	inv, err := e.svc.CreateInvitation(ctx, p, "ana@example.com", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AcceptInvitation(ctx, AcceptInput{Token: inv.Token, Password: goodPW}, ClientMeta{})
	var mr *MFARequiredError
	if !errors.As(err, &mr) {
		t.Fatalf("accepting as an MFA user = %v, want MFARequiredError (no session without the second factor)", err)
	}
}
