// Package vault stores credentials encrypted at rest (AES-256-GCM, envelope
// encryption) and hands them out only inside a callback. It is the ONLY code
// allowed to touch the credentials table (see architecture_test.go).
package vault

import "log/slog"

// Secret holds sensitive bytes. It never serializes or prints its content:
// fmt, JSON and slog all see "[REDACTED]".
type Secret struct{ b []byte }

// NewSecret copies b into a Secret.
func NewSecret(b []byte) Secret { return Secret{b: append([]byte(nil), b...)} }

// SecretFromString is NewSecret for strings.
func SecretFromString(s string) Secret { return Secret{b: []byte(s)} }

// Reveal returns the raw bytes. Call it only to build an outgoing provider
// request; never log, store or return the result.
func (s Secret) Reveal() []byte { return s.b }

// Len is the secret length in bytes.
func (s Secret) Len() int { return len(s.b) }

// Zero overwrites the content.
func (s *Secret) Zero() {
	for i := range s.b {
		s.b[i] = 0
	}
	s.b = nil
}

func (Secret) String() string               { return "[REDACTED]" }
func (Secret) GoString() string             { return "[REDACTED]" }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
