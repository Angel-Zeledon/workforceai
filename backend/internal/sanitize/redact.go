// Package sanitize turns raw third-party content (emails, API responses) into
// text that is safe to hand to a model: normalized, redacted, neutralized,
// truncated, scored for prompt injection and wrapped as delimited DATA.
package sanitize

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Suspects is a thread-safe set of exact secret values (OAuth tokens, API
// keys loaded in memory) that must never appear in any output. It implements
// vault.Registrar.
type Suspects struct {
	mu   sync.RWMutex
	vals map[string]struct{}
}

// NewSuspects creates an empty set.
func NewSuspects() *Suspects { return &Suspects{vals: map[string]struct{}{}} }

// Add registers an exact secret (values under 8 bytes are ignored: too noisy).
func (s *Suspects) Add(v string) {
	if s == nil || len(v) < 8 {
		return
	}
	s.mu.Lock()
	s.vals[v] = struct{}{}
	s.mu.Unlock()
}

// Redact replaces every registered secret in text.
func (s *Suspects) Redact(text string) string {
	if s == nil {
		return text
	}
	s.mu.RLock()
	vals := make([]string, 0, len(s.vals))
	for v := range s.vals {
		vals = append(vals, v)
	}
	s.mu.RUnlock()
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		if strings.Contains(text, v) {
			text = strings.ReplaceAll(text, v, "[REDACTED:secret]")
		}
	}
	return text
}

type pattern struct {
	kind string
	re   *regexp.Regexp
}

var secretPatterns = []pattern{
	{"private_key", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\b`)},
	{"github_token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`)},
	{"aws_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"google_token", regexp.MustCompile(`\bya29\.[A-Za-z0-9_.-]{20,}|\b1//[A-Za-z0-9_-]{20,}`)},
	{"api_key", regexp.MustCompile(`\b(?:sk|pk|rk)[-_](?:live|test|proj|ant)?[-_]?[A-Za-z0-9]{20,}\b`)},
	{"bearer", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{"secret_assignment", regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|contraseña|clave)\s*[:=]\s*["']?[^\s"'<>]{6,}`)},
	{"token_url", regexp.MustCompile(`(?i)https?://[^\s<>"')]*[?&](?:token|access_token|auth|key|sig|signature|reset|magic|code)=[^\s<>"')&]+[^\s<>"')]*`)},
	{"otp", regexp.MustCompile(`(?i)\b(?:otp|verification code|código de verificación|codigo de verificacion|security code|one[- ]time (?:code|password)|código)\D{0,20}\b\d{4,8}\b`)},
}

var (
	cardRe    = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	phoneRe   = regexp.MustCompile(`(?:\+\d{1,3}[ .-]?)?(?:\(\d{2,4}\)[ .-]?)?\b\d{2,4}[ .-]\d{3,4}[ .-]\d{3,4}\b`)
	ibanRe    = regexp.MustCompile(`\b[A-Z]{2}\d{2}[ ]?(?:[A-Z0-9]{4}[ ]?){3,7}[A-Z0-9]{1,4}\b`)
	longNumRe = regexp.MustCompile(`\b\d{9,}\b`)
	taxIDRe   = regexp.MustCompile(`\b[A-ZÑ&]{3,4}\d{6}[A-Z0-9]{3}\b`)
)

// RedactSecrets masks credentials, token links, OTP codes and card numbers.
// It always runs, whatever the redaction profile.
func RedactSecrets(text string, s *Suspects) string {
	text = s.Redact(text)
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllString(text, "[REDACTED:"+p.kind+"]")
	}
	text = cardRe.ReplaceAllStringFunc(text, func(m string) string {
		digits := onlyDigits(m)
		if len(digits) >= 13 && len(digits) <= 19 && luhn(digits) {
			return "[REDACTED:card]"
		}
		return m
	})
	return text
}

// RedactPII additionally masks tax ids, bank accounts and phone numbers
// (profile "strict").
func RedactPII(text string) string {
	text = ibanRe.ReplaceAllString(text, "[REDACTED:account]")
	text = taxIDRe.ReplaceAllString(text, "[REDACTED:tax_id]")
	text = longNumRe.ReplaceAllString(text, "[REDACTED:number]")
	text = phoneRe.ReplaceAllString(text, "[REDACTED:phone]")
	return text
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhn(d string) bool {
	sum, alt := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}
