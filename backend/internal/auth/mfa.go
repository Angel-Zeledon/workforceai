package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Two-factor authentication (TOTP).
//
// Enabling: Setup stores a pending secret (encrypted at rest), the user proves the app works by sending a valid code (Enable),
// and receives single-use recovery codes ONCE. From then on, Login with a correct password does not open a session: it
// returns an MFARequiredError carrying a short-lived challenge, and the session opens only with VerifyMFA (a TOTP code or a
// recovery code). The challenge is NOT a JWT (it cannot be used as an access token). A used time step is remembered so the
// same code cannot be replayed, and wrong codes count toward the same lockout as wrong passwords.

var (
	// ErrMFAAlreadyEnabled is returned by Setup when two-factor is already on.
	ErrMFAAlreadyEnabled = errors.New("auth: mfa already enabled")
	// ErrMFANotPending is returned by Enable when Setup was not called first.
	ErrMFANotPending = errors.New("auth: no pending mfa setup")
	// ErrMFAInvalidCode is returned for a wrong or reused code.
	ErrMFAInvalidCode = errors.New("auth: invalid mfa code")
)

// MFARequiredError is returned (instead of a session) when the password was right but a second factor is needed.
type MFARequiredError struct{ Token string }

func (e *MFARequiredError) Error() string { return "auth: second factor required" }

// MFARecord is the stored state of a user's second factor.
type MFARecord struct {
	UserID         string
	SecretEnc      string    // AES-256-GCM(secret), base64; bound to the user id
	EnabledAt      time.Time // zero while pending
	LastStep       int64     // last accepted time step (replay protection)
	RecoveryHashes []string  // SHA-256 of the unused recovery codes
}

// Enabled reports whether the second factor is active (a pending setup is not).
func (r MFARecord) Enabled() bool { return !r.EnabledAt.IsZero() }

const (
	mfaChallengeTTL = 5 * time.Minute
	mfaIssuer       = "AI Workforce OS"
	recoveryCodes   = 8
)

// ---- secret encryption (key derived from the server secret, so no new variable is required) ----

func (s *Service) mfaKey() []byte {
	if len(s.cfg.MFAKey) >= 32 {
		return s.cfg.MFAKey[:32]
	}
	m := hmac.New(sha256.New, s.cfg.Secret)
	m.Write([]byte("wf-mfa-secret-v1"))
	return m.Sum(nil)
}

func (s *Service) sealSecret(userID string, secret []byte) (string, error) {
	blk, err := aes.NewCipher(s.mfaKey())
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(nonce, nonce, secret, []byte(userID))), nil
}

func (s *Service) openSecret(userID, enc string) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(s.mfaKey())
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	if len(raw) < g.NonceSize() {
		return nil, errors.New("auth: mfa secret is corrupt")
	}
	return g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(userID))
}

// ---- the login challenge: userID|orgID|expiry, MAC'd; deliberately not a JWT ----

func (s *Service) challengeMAC(payload string) []byte {
	m := hmac.New(sha256.New, s.cfg.Secret)
	m.Write([]byte("wf-mfa-challenge-v1|" + payload))
	return m.Sum(nil)
}

func (s *Service) newChallenge(userID, orgID string, now time.Time) string {
	payload := userID + "|" + orgID + "|" + strconv.FormatInt(now.Add(mfaChallengeTTL).Unix(), 10)
	return b64url.EncodeToString([]byte(payload)) + "." + b64url.EncodeToString(s.challengeMAC(payload))
}

func (s *Service) parseChallenge(tok string, now time.Time) (userID, orgID string, err error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 2 || len(tok) > 400 {
		return "", "", ErrInvalidToken
	}
	pb, err1 := b64url.DecodeString(parts[0])
	mb, err2 := b64url.DecodeString(parts[1])
	if err1 != nil || err2 != nil || !hmac.Equal(mb, s.challengeMAC(string(pb))) {
		return "", "", ErrInvalidToken
	}
	f := strings.Split(string(pb), "|")
	if len(f) != 3 {
		return "", "", ErrInvalidToken
	}
	exp, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return "", "", ErrInvalidToken
	}
	if now.Unix() >= exp {
		return "", "", ErrTokenExpired
	}
	return f[0], f[1], nil
}

// mfaGate returns the challenge error when the user has a second factor on; nil otherwise.
func (s *Service) mfaGate(ctx context.Context, u User, orgID string) error {
	r, err := s.store.GetMFA(ctx, u.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !r.Enabled() {
		return nil
	}
	return &MFARequiredError{Token: s.newChallenge(u.ID, orgID, s.now())}
}

// ---- enrolment ----

// MFASetupInfo is shown to the user once, to add the account to an authenticator app.
type MFASetupInfo struct {
	Secret string `json:"secret"`      // base32, for manual entry
	URI    string `json:"otpauth_uri"` // for the QR code
}

// MFASetup starts (or restarts) enrolment with a fresh pending secret.
func (s *Service) MFASetup(ctx context.Context, userID string) (*MFASetupInfo, error) {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if r, err := s.store.GetMFA(ctx, userID); err == nil && r.Enabled() {
		return nil, ErrMFAAlreadyEnabled
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return nil, err
	}
	enc, err := s.sealSecret(userID, secret)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveMFA(ctx, MFARecord{UserID: userID, SecretEnc: enc}); err != nil {
		return nil, err
	}
	return &MFASetupInfo{Secret: encodeSecret(secret), URI: otpauthURI(mfaIssuer, u.Email, secret)}, nil
}

// MFAEnable turns the second factor on after the user proves their app produces valid codes. It returns the recovery
// codes, which are never shown again.
func (s *Service) MFAEnable(ctx context.Context, userID, code string) ([]string, error) {
	r, err := s.store.GetMFA(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrMFANotPending
	}
	if err != nil {
		return nil, err
	}
	if r.Enabled() {
		return nil, ErrMFAAlreadyEnabled
	}
	secret, err := s.openSecret(userID, r.SecretEnc)
	if err != nil {
		return nil, err
	}
	step, ok := matchTOTP(secret, code, s.now())
	if !ok {
		return nil, ErrMFAInvalidCode
	}
	codes, hashes := newRecoveryCodes()
	r.EnabledAt, r.LastStep, r.RecoveryHashes = s.now(), step, hashes
	if err := s.store.SaveMFA(ctx, r); err != nil {
		return nil, err
	}
	s.audit2(ctx, userID, "user.mfa_enabled")
	return codes, nil
}

// MFADisable turns it off. It needs the password and a current code (or a recovery code).
func (s *Service) MFADisable(ctx context.Context, userID, password, code string) error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if ok, _, verr := s.hasher.Verify(password, u.PasswordHash); verr != nil || !ok {
		return ErrInvalidCredentials
	}
	if err := s.checkSecondFactor(ctx, u.ID, code); err != nil {
		return err
	}
	if err := s.store.DeleteMFA(ctx, userID); err != nil {
		return err
	}
	s.audit2(ctx, userID, "user.mfa_disabled")
	return nil
}

// VerifyMFA completes a login that returned MFARequiredError.
func (s *Service) VerifyMFA(ctx context.Context, challenge, code string, meta ClientMeta) (*Session, error) {
	userID, orgID, err := s.parseChallenge(challenge, s.now())
	if err != nil {
		return nil, err
	}
	if s.cfg.LoginLimiter != nil {
		res, lerr := s.cfg.LoginLimiter.Allow(ctx, "mfa:user:"+hashToken(userID), s.cfg.LoginLimit)
		if lerr != nil {
			return nil, fmt.Errorf("auth: mfa limiter: %w", lerr)
		}
		if !res.Allowed {
			return nil, &RateLimitedError{RetryAfter: res.RetryAfter}
		}
	}
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, ErrInvalidToken
	}
	now := s.now()
	if u.LockedUntil.After(now) {
		return nil, &LockedError{Until: u.LockedUntil}
	}
	if err := s.checkSecondFactor(ctx, u.ID, code); err != nil {
		if errors.Is(err, ErrMFAInvalidCode) {
			upd, ferr := s.store.RecordLoginFailure(ctx, u.ID, s.cfg.MaxFailedAttempts, s.cfg.LockDuration, now)
			if ferr == nil && upd.LockedUntil.After(now) {
				return nil, &LockedError{Until: upd.LockedUntil}
			}
		}
		return nil, err
	}
	if u.FailedAttempts > 0 || !u.LockedUntil.IsZero() {
		_ = s.store.ResetLoginFailures(ctx, u.ID)
	}
	m, err := s.pickMembership(ctx, u.ID, orgID)
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, u, m, uuid.NewString(), meta)
}

// checkSecondFactor accepts a TOTP code (once per time step) or an unused recovery code.
func (s *Service) checkSecondFactor(ctx context.Context, userID, code string) error {
	r, err := s.store.GetMFA(ctx, userID)
	if errors.Is(err, ErrNotFound) || (err == nil && !r.Enabled()) {
		return ErrMFAInvalidCode
	}
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if secret, oerr := s.openSecret(userID, r.SecretEnc); oerr == nil {
		if step, ok := matchTOTP(secret, code, s.now()); ok {
			fresh, uerr := s.store.UseMFAStep(ctx, userID, step)
			if uerr != nil {
				return uerr
			}
			if fresh {
				return nil
			}
			return ErrMFAInvalidCode // the same code was already used
		}
	}
	if len(code) >= 8 { // recovery codes look like xxxxx-xxxxx
		ok, cerr := s.store.ConsumeRecoveryCode(ctx, userID, hashToken(normRecovery(code)))
		if cerr != nil {
			return cerr
		}
		if ok {
			s.audit2(ctx, userID, "user.mfa_recovery_used")
			return nil
		}
	}
	return ErrMFAInvalidCode
}

// MFAEnabled reports whether the user has an active second factor.
func (s *Service) MFAEnabled(ctx context.Context, userID string) bool {
	r, err := s.store.GetMFA(ctx, userID)
	return err == nil && r.Enabled()
}

const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func normRecovery(c string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(c), " ", ""))
}

func newRecoveryCodes() (codes, hashes []string) {
	for i := 0; i < recoveryCodes; i++ {
		var b [10]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("auth: crypto/rand unavailable")
		}
		var sb strings.Builder
		for j, v := range b {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
		}
		c := sb.String()
		codes = append(codes, c)
		hashes = append(hashes, hashToken(normRecovery(c)))
	}
	return codes, hashes
}

func (s *Service) audit2(ctx context.Context, userID, action string) {
	if s.cfg.Audit == nil {
		return
	}
	orgID := ""
	if ms, err := s.store.MembershipsOf(ctx, userID); err == nil && len(ms) > 0 {
		orgID = ms[0].OrgID
	}
	s.cfg.Audit(ctx, AuditEvent{OrgID: orgID, Actor: userID, Action: action, Entity: "user", EntityID: userID})
}
