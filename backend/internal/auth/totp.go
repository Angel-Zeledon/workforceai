package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 / RFC 4226 define TOTP over HMAC-SHA1; every authenticator app expects it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238): 6 digits, 30-second steps, HMAC-SHA1 (what Google Authenticator, Microsoft Authenticator, 1Password
// and Authy all implement). Standard library only.

const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew is how many steps either side of "now" are accepted (clock drift between phone and server).
	totpSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh 160-bit secret.
func newTOTPSecret() ([]byte, error) {
	b := make([]byte, 20)
	_, err := rand.Read(b)
	return b, err
}

func encodeSecret(b []byte) string { return b32.EncodeToString(b) }

// totpAt computes the code of a time step.
func totpAt(secret []byte, step int64) string {
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(ctr[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])) % 1_000_000
	return fmt.Sprintf("%0*d", totpDigits, code)
}

func stepOf(t time.Time) int64 { return t.Unix() / int64(totpPeriod/time.Second) }

// matchTOTP reports the step whose code equals the input within the skew window, if any. The comparison is constant-time.
func matchTOTP(secret []byte, input string, now time.Time) (int64, bool) {
	input = strings.ReplaceAll(strings.TrimSpace(input), " ", "")
	if len(input) != totpDigits {
		return 0, false
	}
	cur := stepOf(now)
	found := int64(-1)
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		if subtle.ConstantTimeCompare([]byte(totpAt(secret, s)), []byte(input)) == 1 {
			found = s
		}
	}
	return found, found >= 0
}

// otpauthURI is what an authenticator app reads from the QR code (or what the user can paste).
func otpauthURI(issuer, account string, secret []byte) string {
	v := url.Values{}
	v.Set("secret", encodeSecret(secret))
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(int(totpPeriod/time.Second)))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}
