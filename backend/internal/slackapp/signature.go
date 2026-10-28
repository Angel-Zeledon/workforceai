// Package slackapp is the two-way Slack channel for humans (docs/architecture/channels.md): an organization installs
// its own Slack app (bring-your-own: signing secret + bot token, sealed with the vault), approval requests are posted
// as Block Kit messages with Approve / Reject buttons, and people query the office with a slash command
// (/oficina estado, /oficina pendientes, /oficina vincular <code>).
//
// It is NOT the Slack connector of the agents (internal/connections/slack): that one is a tool the agents use through
// the Tool Gateway with its own credential; this one is a channel for people and never shares credentials with it.
//
// Security model, in order, for every inbound request:
//  1. the body is capped and the Slack v0 signature is verified (HMAC-SHA256 with the organization's signing secret,
//     5-minute replay window, constant-time comparison) before anything is parsed;
//  2. the workspace (team_id) must be the one the bot token belongs to;
//  3. the Slack user is mapped to a platform user (explicit one-time link code, or a verified e-mail on both sides);
//     the identity in the payload alone is never enough;
//  4. the platform user's CURRENT membership role is read and the decision goes through the same application path as
//     POST /approvals/{id}/decision (required role, double approval, no self-approval, agents never decide);
//  5. the kill switch and read-only mode refuse approvals from Slack (a rejection only stops work and is allowed);
//  6. retried / double-clicked interactions are deduplicated and every decision is audited with channel=slack.
package slackapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// MaxSkew is the replay window of a signed request: Slack's own recommendation (5 minutes).
const MaxSkew = 5 * time.Minute

// MaxBodyBytes caps an inbound request before it is verified (Slack payloads are a few KB).
const MaxBodyBytes = 64 << 10

// Signature errors. None of them says which part failed to the caller beyond these codes.
var (
	ErrNoSignature  = errors.New("slackapp: missing signature headers")
	ErrStale        = errors.New("slackapp: request timestamp outside the replay window")
	ErrBadSignature = errors.New("slackapp: signature mismatch")
)

// Sign returns the v0 signature of body at timestamp ts (tests and the documentation use it).
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":"))
	m.Write(body)
	return "v0=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks X-Slack-Request-Timestamp / X-Slack-Signature against the raw body:
// v0=HMAC-SHA256(secret, "v0:" + timestamp + ":" + body). The timestamp must be within MaxSkew of now (both
// directions) so a captured request cannot be replayed later, and the comparison is constant-time.
func Verify(secret, ts, sig string, body []byte, now time.Time) error {
	if secret == "" || ts == "" || sig == "" {
		return ErrNoSignature
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return ErrNoSignature
	}
	if d := now.Sub(time.Unix(sec, 0)); d > MaxSkew || d < -MaxSkew {
		return ErrStale
	}
	want := Sign(secret, strings.TrimSpace(ts), body)
	if !hmac.Equal([]byte(want), []byte(strings.TrimSpace(sig))) {
		return ErrBadSignature
	}
	return nil
}
