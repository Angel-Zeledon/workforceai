package meetings

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Video links. The platform runs no media stack: a meeting carries a link to a
// provider. Jitsi links are generated (the base URL is configurable, so a
// self-hosted Jitsi works); Meet / Zoom / Teams / any other link is attached
// after validating that it is a plain https URL of the provider it claims.

// DefaultJitsiBase is the public Jitsi Meet service.
const DefaultJitsiBase = "https://meet.jit.si"

// NormalizeJitsiBase validates the base URL of a Jitsi server: https, a host, no
// credentials, query or fragment. A path is kept (some installs live under /jitsi).
func NormalizeJitsiBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultJitsiBase, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: jitsi_base_url must be an https URL without credentials", ErrInvalid)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// roomSlug makes the readable part of a room name (letters and digits only).
func roomSlug(title string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "meeting"
	}
	return s
}

// JitsiURL builds an unguessable Jitsi room URL: the random suffix makes the
// room private in practice (Jitsi rooms are created by whoever opens the link).
func JitsiURL(base, title string) (string, error) {
	b, err := NormalizeJitsiBase(base)
	if err != nil {
		return "", err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	return b + "/" + roomSlug(title) + "-" + hex.EncodeToString(rnd[:]), nil
}

// hostMatches reports whether host equals one of the suffixes or is a subdomain of it.
func hostMatches(host string, suffixes ...string) bool {
	host = strings.ToLower(host)
	for _, s := range suffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// ValidateLink checks that link is an https URL fitting the provider and returns it
// normalized. Jitsi links must live on the configured Jitsi host.
func ValidateLink(provider, link, jitsiBase string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", fmt.Errorf("%w: join_url is required", ErrInvalid)
	}
	if len(link) > 2000 {
		return "", fmt.Errorf("%w: join_url is too long", ErrInvalid)
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("%w: join_url must be an https URL", ErrInvalid)
	}
	host := u.Hostname()
	ok := true
	switch provider {
	case ProviderMeet:
		ok = hostMatches(host, "meet.google.com")
	case ProviderZoom:
		ok = hostMatches(host, "zoom.us", "zoom.com", "zoomgov.com")
	case ProviderTeams:
		ok = hostMatches(host, "teams.microsoft.com", "teams.live.com", "teams.microsoft.us")
	case ProviderJitsi:
		b, err := NormalizeJitsiBase(jitsiBase)
		if err != nil {
			return "", err
		}
		bu, _ := url.Parse(b)
		ok = strings.EqualFold(host, bu.Hostname())
	case ProviderCustom:
	default:
		return "", fmt.Errorf("%w: provider %q takes no link", ErrInvalid, provider)
	}
	if !ok {
		return "", fmt.Errorf("%w: the link does not belong to %s", ErrInvalid, provider)
	}
	return u.String(), nil
}

// validProvider lists the providers a meeting may use.
func validProvider(p string) bool {
	switch p {
	case ProviderJitsi, ProviderMeet, ProviderZoom, ProviderTeams, ProviderCustom, ProviderNone:
		return true
	}
	return false
}

// Embeddable reports whether the app may show the link inside an iframe: only a
// Jitsi room on the organization's own Jitsi base (Zoom, Meet and Teams refuse
// to be framed and open in a new tab).
func Embeddable(provider, link, jitsiBase string) bool {
	if provider != ProviderJitsi || link == "" {
		return false
	}
	_, err := ValidateLink(ProviderJitsi, link, jitsiBase)
	return err == nil
}
