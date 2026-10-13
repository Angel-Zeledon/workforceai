package webtool

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

// Fetch limits. Hard-coded ceilings: settings and env can only go lower.
const (
	MaxRedirects    = 3
	DefaultMaxBytes = 2 << 20
	DefaultMaxText  = 24000
	userAgent       = "AIWorkforceBot/1.0 (+read-only fetch for an AI assistant)"
)

// Typed refusals.
var (
	ErrContentType = errors.New("content_type_not_allowed")
	ErrPDF         = errors.New("pdf_not_supported_yet")
	ErrTooManyHops = errors.New("too_many_redirects")
	ErrRedirect    = errors.New("redirect_invalid")
	ErrStatus      = errors.New("http_status")
	ErrFetch       = errors.New("fetch_failed")
	ErrTimeout     = errors.New("timeout")
)

// Page is a fetched page reduced to text.
type Page struct {
	URL         string // the final URL (after redirects)
	Title       string
	Text        string
	ContentType string
	Status      int
	Bytes       int
	Truncated   bool // the body hit the size cap or the text hit the length cap
	FetchedAt   time.Time
	Hash        string // sha256 of the extracted text, hex
	Redirects   int
}

// Fetcher fetches public pages. It never executes anything it downloads.
type Fetcher struct {
	Guard NetGuard
	// Domain applies the organization/operator domain policy to the host of the request and of every redirect.
	Domain func(host string) error
	// Timeout is the total budget of one fetch (all hops).
	Timeout  time.Duration
	MaxBytes int64
	MaxText  int
	Now      func() time.Time

	// Test seams. Transport replaces the network (the guard still validates every URL); Dial replaces the raw
	// socket dial of the pinned transport (the guard still validates the resolved addresses).
	Transport http.RoundTripper
	Dial      func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

// transport builds the pinned transport: the only place that opens sockets. It resolves the name itself,
// validates ALL answers and dials the validated IP, so there is no second resolution to rebind.
func (f *Fetcher) transport() http.RoundTripper {
	if f.Transport != nil {
		return f.Transport
	}
	d := &net.Dialer{Timeout: 5 * time.Second, Control: f.Guard.ControlFunc()}
	return &http.Transport{
		Proxy:                  nil, // never the environment proxy: it would bypass the guard
		DisableKeepAlives:      true,
		DisableCompression:     false, // gzip is decoded by the transport; the cap is applied to the DECODED bytes
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    8 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, ErrBadURL
			}
			addrs, err := f.Guard.Resolve(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error = ErrFetch
			for _, a := range addrs {
				target := net.JoinHostPort(a.String(), port)
				var c net.Conn
				if f.Dial != nil {
					c, err = f.Dial(ctx, network, target)
				} else {
					c, err = d.DialContext(ctx, network, target)
				}
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
}

var allowedTypes = map[string]bool{
	"text/html": true, "application/xhtml+xml": true, "text/plain": true, "text/markdown": true, "text/csv": true,
	"application/json": true, "text/json": true, "application/xml": true, "text/xml": true,
	"application/rss+xml": true, "application/atom+xml": true,
}

func typeAllowed(ct string) bool {
	return allowedTypes[ct] || strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

// Fetch downloads rawURL (GET only) and returns its readable text.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Page, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	maxBytes := f.MaxBytes
	if maxBytes <= 0 || maxBytes > DefaultMaxBytes {
		maxBytes = DefaultMaxBytes
	}
	maxText := f.MaxText
	if maxText <= 0 || maxText > DefaultMaxText {
		maxText = DefaultMaxText
	}
	rt := f.transport()
	if t, ok := rt.(*http.Transport); ok {
		defer t.CloseIdleConnections()
	}
	cur := rawURL
	for hop := 0; ; hop++ {
		u, host, err := f.Guard.CheckURL(cur)
		if err != nil {
			return Page{}, err
		}
		if f.Domain != nil {
			if err := f.Domain(host); err != nil {
				return Page{}, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return Page{}, ErrBadURL
		}
		// Only these headers ever leave: no cookies, no Authorization, no Referer.
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
		req.Header.Set("Accept-Language", "en,es;q=0.8,pt;q=0.6")
		resp, err := rt.RoundTrip(req)
		if err != nil {
			return Page{}, classify(ctx, err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != 304 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if hop >= MaxRedirects {
				return Page{}, ErrTooManyHops
			}
			if loc == "" {
				return Page{}, ErrRedirect
			}
			next, err := u.Parse(loc)
			if err != nil {
				return Page{}, ErrRedirect
			}
			cur = next.String() // validated again at the top of the loop
			continue
		}
		page, err := f.read(resp, u, maxBytes, maxText)
		if err != nil {
			return Page{}, classify(ctx, err)
		}
		page.Redirects = hop
		return page, nil
	}
}

func classify(ctx context.Context, err error) error {
	for _, known := range []error{ErrBadURL, ErrScheme, ErrCredentials, ErrPort, ErrHost, ErrBlockedAddr, ErrResolve, ErrDomainPolicy, ErrTooLong,
		ErrContentType, ErrPDF, ErrTooManyHops, ErrRedirect, ErrStatus} {
		if errors.Is(err, known) {
			return err
		}
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	return ErrFetch // never leak raw network errors (they can describe the internal network)
}

func (f *Fetcher) read(resp *http.Response, u *url.URL, maxBytes int64, maxText int) (Page, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Page{}, fmt.Errorf("%w: %d", ErrStatus, resp.StatusCode)
	}
	ct := ""
	params := map[string]string{}
	if v := resp.Header.Get("Content-Type"); v != "" {
		if mt, p, err := mime.ParseMediaType(v); err == nil {
			ct, params = strings.ToLower(mt), p
		}
	}
	if ct == "application/pdf" {
		return Page{}, ErrPDF
	}
	if ct != "" && !typeAllowed(ct) && ct != "application/octet-stream" {
		return Page{}, ErrContentType
	}
	// The cap applies to the decoded body: a gzip bomb stops here, not in memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return Page{}, err
	}
	truncated := int64(len(body)) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	if ct == "" || ct == "application/octet-stream" {
		sniff := strings.ToLower(strings.SplitN(httpDetect(body), ";", 2)[0])
		if sniff == "application/pdf" {
			return Page{}, ErrPDF
		}
		if !typeAllowed(sniff) {
			return Page{}, ErrContentType
		}
		ct = sniff
	}
	text := decode(body, params["charset"], ct)
	var title, out string
	switch {
	case ct == "text/html" || ct == "application/xhtml+xml":
		title, out = ExtractHTML(text, u)
	default:
		out = strings.TrimSpace(text)
	}
	rs := []rune(out)
	if len(rs) > maxText {
		out, truncated = string(rs[:maxText]), true
	}
	sum := sha256.Sum256([]byte(out))
	return Page{URL: u.String(), Title: title, Text: out, ContentType: ct, Status: resp.StatusCode, Bytes: len(body),
		Truncated: truncated, FetchedAt: f.now(), Hash: hex.EncodeToString(sum[:])}, nil
}

func httpDetect(b []byte) string {
	if len(b) > 512 {
		b = b[:512]
	}
	return http.DetectContentType(b)
}

// decode returns UTF-8 text. Unknown or broken encodings degrade to replacement characters, never to an error.
func decode(body []byte, label, ct string) string {
	if utf8.Valid(body) {
		return string(body)
	}
	enc, _ := charset.Lookup(label)
	if enc == nil {
		_, name, _ := charset.DetermineEncoding(body, ct)
		enc, _ = charset.Lookup(name)
	}
	if enc != nil {
		if out, _, err := transform.Bytes(enc.NewDecoder(), body); err == nil && utf8.Valid(out) {
			return string(out)
		}
	}
	return strings.ToValidUTF8(string(body), "�")
}
