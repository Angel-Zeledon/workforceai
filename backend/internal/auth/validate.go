package auth

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
	maxEmailLen    = 254
	maxNameLen     = 100
)

// NormalizeEmail trims and lowercases an address.
func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// ValidateEmail returns a message when the (normalized) email is unusable.
func ValidateEmail(e string) string {
	if e == "" {
		return "required"
	}
	if len(e) > maxEmailLen {
		return "too long"
	}
	for _, r := range e {
		if r <= ' ' || r == 0x7f || r == '<' || r == '>' || r == ',' || r == ';' || r == '"' {
			return "invalid format"
		}
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return "invalid format"
	}
	at := strings.LastIndexByte(e, '@')
	if at < 1 || !strings.Contains(e[at+1:], ".") {
		return "invalid format"
	}
	return ""
}

var commonPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "passw0rd123": {}, "1234567890": {},
	"qwertyuiop": {}, "qwerty12345": {}, "letmein123": {}, "welcome123": {}, "iloveyou123": {},
	"administrator": {}, "changeme123": {}, "0123456789": {}, "abcdefghij": {},
}

// ValidatePassword returns a message when the password is too weak.
func ValidatePassword(pw, email string) string {
	if !utf8.ValidString(pw) {
		return "invalid encoding"
	}
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLen:
		return "must be at least 10 characters"
	case n > MaxPasswordLen:
		return "must be at most 128 characters"
	}
	lower := strings.ToLower(pw)
	if _, bad := commonPasswords[lower]; bad {
		return "too common"
	}
	if email != "" {
		local := email
		if i := strings.IndexByte(email, '@'); i > 0 {
			local = email[:i]
		}
		if lower == email || lower == local {
			return "must not match the email"
		}
	}
	first := []rune(pw)[0]
	same := true
	var lo, up, dg, ot bool
	for _, r := range pw {
		if r != first {
			same = false
		}
		switch {
		case unicode.IsLower(r):
			lo = true
		case unicode.IsUpper(r):
			up = true
		case unicode.IsDigit(r):
			dg = true
		default:
			ot = true
		}
	}
	if same {
		return "too repetitive"
	}
	classes := 0
	for _, b := range []bool{lo, up, dg, ot} {
		if b {
			classes++
		}
	}
	if classes < 2 {
		return "must mix at least two of: lowercase, uppercase, digits, symbols"
	}
	return ""
}

// ValidateName validates a person or organization display name.
func ValidateName(name string, min int) string {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return "invalid encoding"
	}
	n := utf8.RuneCountInString(name)
	switch {
	case n < min:
		return "too short"
	case n > maxNameLen:
		return "too long"
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "contains control characters"
		}
	}
	return ""
}

// slugify turns a name into a lowercase ASCII slug.
func slugify(name string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "org"
	}
	return s
}
