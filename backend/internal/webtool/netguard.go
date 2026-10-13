// Package webtool is the "web" tool of the agents (docs/architecture/agent-web.md): web search through a
// provider and a fetch of public pages that cannot be turned into an SSRF primitive. Everything that reaches
// the model is text, sanitized and delimited as untrusted data by the caller.
package webtool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/idna"
)

// Stable error codes (what the agent and the audit see; never raw network errors).
var (
	ErrBadURL       = errors.New("url_invalid")
	ErrScheme       = errors.New("url_scheme_not_allowed")
	ErrCredentials  = errors.New("url_credentials_not_allowed")
	ErrPort         = errors.New("url_port_not_allowed")
	ErrHost         = errors.New("host_not_allowed")
	ErrBlockedAddr  = errors.New("address_not_allowed")
	ErrResolve      = errors.New("dns_failed")
	ErrDomainPolicy = errors.New("domain_not_permitted")
	ErrTooLong      = errors.New("url_too_long")
)

// MaxURLLength bounds the URLs that are even looked at.
const MaxURLLength = 2048

// Resolver resolves names. Tests inject a fake (including a rebinding one).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// NetGuard validates URLs and addresses. The zero value allows ports 80 and 443.
type NetGuard struct {
	Ports    []int
	Resolver Resolver
}

func (g NetGuard) ports() []int {
	if len(g.Ports) == 0 {
		return []int{80, 443}
	}
	return g.Ports
}

func (g NetGuard) resolver() Resolver {
	if g.Resolver != nil {
		return g.Resolver
	}
	return net.DefaultResolver
}

var (
	numericTLD  = regexp.MustCompile(`^(0[xX][0-9a-fA-F]*|[0-9]+)$`)
	reHostChars = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
)

// forbiddenSuffixes are names that are internal by convention, whatever they resolve to.
var forbiddenSuffixes = []string{".localhost", ".local", ".internal", ".localdomain", ".lan", ".intranet", ".corp", ".home", ".home.arpa", ".in-addr.arpa", ".ip6.arpa"}

var forbiddenHosts = map[string]bool{"localhost": true, "metadata.google.internal": true, "metadata": true, "instance-data": true}

// CheckURL validates the syntax of a URL and returns it with the normalized (ASCII, lowercase, no trailing
// dot) host. It does NOT resolve the name: see Resolve.
func (g NetGuard) CheckURL(raw string) (*url.URL, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", ErrBadURL
	}
	if len(raw) > MaxURLLength {
		return nil, "", ErrTooLong
	}
	for _, r := range raw {
		if r < 0x21 || r == 0x7f { // controls and spaces
			return nil, "", ErrBadURL
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", ErrBadURL
	}
	if u.Scheme != "http" && u.Scheme != "https" { // url.Parse lowercases the scheme
		return nil, "", ErrScheme
	}
	if u.User != nil || strings.Contains(u.Host, "@") {
		return nil, "", ErrCredentials
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, "", ErrBadURL
	}
	host := u.Hostname()
	if strings.Contains(host, "%") || strings.ContainsAny(host, `\`) {
		return nil, "", ErrHost // zone ids, backslash tricks
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return nil, "", ErrPort
		}
		port = n
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, "", ErrBadURL
	}
	ok := false
	for _, a := range g.ports() {
		ok = ok || a == port
	}
	if !ok {
		return nil, "", ErrPort
	}
	host, err = NormalizeHost(host)
	if err != nil {
		return nil, "", err
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if err := CheckAddr(a); err != nil {
			return nil, "", err
		}
	}
	u.Fragment = ""
	return u, host, nil
}

// NormalizeHost lowercases, strips the trailing dot, converts unicode to punycode and rejects names that are
// ambiguous or internal. IP literals pass through (CheckAddr judges them).
func NormalizeHost(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || strings.HasSuffix(host, ".") {
		return "", ErrHost
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host, nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", ErrHost
	}
	host = strings.TrimSuffix(strings.ToLower(ascii), ".")
	// The IDNA mapping turns full-width digits into ASCII: judge the result again.
	if a, err := netip.ParseAddr(host); err == nil {
		return host, CheckAddr(a)
	}
	if len(host) > 253 || !reHostChars.MatchString(host) {
		return "", ErrHost
	}
	if forbiddenHosts[host] {
		return "", ErrHost
	}
	for _, s := range forbiddenSuffixes {
		if strings.HasSuffix(host, s) {
			return "", ErrHost
		}
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", ErrHost // intranet single-label names
	}
	// 2130706433, 0x7f.1, 0177.0.0.1: legacy IPv4 spellings some resolvers accept. A real TLD is never numeric.
	if numericTLD.MatchString(labels[len(labels)-1]) {
		return "", ErrHost
	}
	for _, l := range labels {
		if len(l) > 63 {
			return "", ErrHost
		}
	}
	return host, nil
}

type prefixList []netip.Prefix

func mustPrefixes(ss ...string) prefixList {
	out := make(prefixList, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func (l prefixList) contains(a netip.Addr) bool {
	for _, p := range l {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var blockedV4 = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"100.100.100.200/32", "192.0.0.192/32", // Alibaba / Oracle metadata
)

var blockedV6 = mustPrefixes(
	"::/96",        // unspecified, loopback and deprecated IPv4-compatible
	"64:ff9b::/96", // NAT64: reaches IPv4 hosts
	"64:ff9b:1::/48",
	"100::/64",                     // discard
	"2001::/32",                    // Teredo
	"2001:db8::/32",                // documentation
	"2001:10::/28", "2001:20::/28", // ORCHID
	"2002::/16", // 6to4: embeds an IPv4 address
	"3fff::/20", // documentation
	"5f00::/16", // SRv6
	"fc00::/7",  // unique local (includes fd00:ec2::254, AWS metadata)
	"fe80::/10", "fec0::/10", "ff00::/8",
)

// CheckAddr rejects every address that is not a plain public unicast address.
func CheckAddr(a netip.Addr) error {
	if !a.IsValid() {
		return ErrBlockedAddr
	}
	if a.Zone() != "" {
		return ErrBlockedAddr
	}
	a = a.Unmap() // ::ffff:127.0.0.1 is 127.0.0.1
	if a.Is4() {
		if blockedV4.contains(a) {
			return fmt.Errorf("%w: %s", ErrBlockedAddr, "ipv4")
		}
	} else {
		if blockedV6.contains(a) {
			return fmt.Errorf("%w: %s", ErrBlockedAddr, "ipv6")
		}
		// Any other embedding of an IPv4 address in the low 32 bits is judged as that address.
		b := a.As16()
		if allZero(b[:10]) && b[10] == 0xff && b[11] == 0xff { // unreachable after Unmap; defensive
			return ErrBlockedAddr
		}
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return ErrBlockedAddr
	}
	return nil
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Resolve resolves host (a normalized name or an IP literal) and returns the addresses to dial. ALL the
// answers must be public: one private record rejects the host (an attacker controls DNS and could race).
func (g NetGuard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		if err := CheckAddr(a); err != nil {
			return nil, err
		}
		return []netip.Addr{a.Unmap()}, nil
	}
	if _, err := NormalizeHost(host); err != nil {
		return nil, err
	}
	addrs, err := g.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, ErrResolve
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if err := CheckAddr(a); err != nil {
			return nil, err
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

// ControlFunc is the last line of defense, run by the dialer on the final socket address: even if every other
// check had a bug, a connection to a forbidden address is refused by the kernel call itself.
func (g NetGuard) ControlFunc() func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		h, p, err := net.SplitHostPort(address)
		if err != nil {
			return ErrBlockedAddr
		}
		a, err := netip.ParseAddr(h)
		if err != nil {
			return ErrBlockedAddr
		}
		if err := CheckAddr(a); err != nil {
			return err
		}
		n, _ := strconv.Atoi(p)
		for _, ok := range g.ports() {
			if ok == n {
				return nil
			}
		}
		return ErrPort
	}
}
