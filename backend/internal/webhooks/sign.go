package webhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Header names of a delivery.
const (
	HeaderSignature = "X-AIWOS-Signature" // t=<unix seconds>,v1=<hex hmac>
	HeaderEvent     = "X-AIWOS-Event"
	HeaderDelivery  = "X-AIWOS-Delivery"
)

// SecretPrefix marks signing secrets so they are recognizable in leak scanners.
const SecretPrefix = "whsec_"

// NewSecret returns a fresh signing secret (shown once to the administrator).
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Sign returns the value of the signature header for body at time ts:
//
//	t=<ts>,v1=hex(HMAC-SHA256(secret, "<ts>." + body))
//
// Binding the timestamp into the MAC lets receivers reject replays.
func Sign(secret string, ts time.Time, body []byte) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	return "t=" + t + ",v1=" + mac(secret, t, body)
}

func mac(secret, t string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(t))
	h.Write([]byte("."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verification errors.
var (
	ErrBadSignature = errors.New("webhooks: signature mismatch")
	ErrStale        = errors.New("webhooks: timestamp outside tolerance")
	ErrMalformed    = errors.New("webhooks: malformed signature header")
)

// Verify checks a signature header (what a receiver does). tolerance bounds the clock skew / replay window;
// the comparison is constant time. Several v1= values are accepted (secret rotation).
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var t string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrMalformed
		}
		switch k {
		case "t":
			t = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(t, 10, 64)
	if err != nil || len(sigs) == 0 {
		return ErrMalformed
	}
	d := now.Sub(time.Unix(sec, 0))
	if d < 0 {
		d = -d
	}
	if tolerance > 0 && d > tolerance {
		return ErrStale
	}
	want := mac(secret, t, body)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrBadSignature
}
