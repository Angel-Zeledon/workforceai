package customapi

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/webtool"
)

// Ports a source may live on.
var Ports = []int{80, 443, 8080, 8443}

// Transport is the base transport of generic sources. Their URL is written by
// an admin, so by default it must not become a way into the server's own
// network: every name is resolved here, ALL answers must be public and the
// dialer re-checks the final address (no DNS rebinding window). allowPrivate
// (self-hosted, CUSTOM_API_ALLOW_PRIVATE) lets an organization reach an ERP on
// its own LAN; it never changes the GET-only rule. Errors carry no address.
func Transport(allowPrivate bool) http.RoundTripper {
	t := &http.Transport{
		Proxy:                  nil, // never the environment proxy: it would bypass the guard
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    8 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        30 * time.Second,
	}
	if allowPrivate {
		t.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
		return t
	}
	guard := webtool.NetGuard{Ports: Ports}
	d := &net.Dialer{Timeout: 5 * time.Second, Control: guard.ControlFunc()}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, &connections.ProviderError{Code: CodeBlocked}
		}
		addrs, err := guard.Resolve(ctx, host)
		if err != nil {
			return nil, &connections.ProviderError{Code: CodeBlocked}
		}
		var last error = &connections.ProviderError{Code: connections.CodeProviderError}
		for _, a := range addrs {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
	return t
}

// CodeBlocked: the source's address is private or otherwise not allowed.
const CodeBlocked = "address_not_allowed"
