package linkimports

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"aiworkforce/backend/internal/webtool"
)

// Fetch limits.
const (
	MaxRedirects   = 5
	DefaultTimeout = 60 * time.Second
	userAgent      = "AIWorkforceLinkImport/1.0 (+scheduled read-only download of a data link)"
)

// Ports a link may live on (the same as the generic API sources).
var Ports = []int{80, 443, 8080, 8443}

// AllowedExts is the type allowlist of a downloaded file. Legacy binary .xls is deliberately absent: the drive
// does not store it (it can carry macros and is not indexed); export it as .xlsx or CSV.
var AllowedExts = []string{".csv", ".tsv", ".json", ".xlsx", ".txt", ".pdf"}

// FetchError is a refused or failed download with a stable code.
type FetchError struct {
	Code   string
	Status int // the HTTP status when Code is http_status
}

func (e *FetchError) Error() string { return "linkimports: " + e.Code }

func ferr(code string) error { return &FetchError{Code: code} }

// CodeOf returns the stable code of a fetch error ("fetch_failed" when unknown).
func CodeOf(err error) (string, int) {
	var fe *FetchError
	if errors.As(err, &fe) {
		return fe.Code, fe.Status
	}
	for _, e := range []error{webtool.ErrBadURL, webtool.ErrScheme, webtool.ErrCredentials, webtool.ErrPort, webtool.ErrHost,
		webtool.ErrBlockedAddr, webtool.ErrResolve, webtool.ErrTooLong} {
		if errors.Is(err, e) {
			return e.Error(), 0
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimeout, 0
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return CodeTimeout, 0
	}
	return CodeFetch, 0
}

// Header is the optional auth header of a download.
type Header struct{ Name, Value string }

// Download is a fetched file.
type Download struct {
	Data     []byte
	Ext      string // one of AllowedExts
	Mime     string // the Content-Type the server announced (without parameters)
	FinalURL string
	Hops     int
}

// Fetcher downloads links. It never executes or interprets what it downloads.
type Fetcher struct {
	Guard webtool.NetGuard
	// AllowPrivate lets a self-hosted install reach its own network (CUSTOM_API_ALLOW_PRIVATE). It never changes
	// the scheme rule, the size cap or the type allowlist.
	AllowPrivate bool
	// AllowHTTP accepts plain http links (LINK_IMPORTS_ALLOW_HTTP). Off by default: a link that carries a token or
	// business data should not travel in clear text, and an https link may never redirect to http.
	AllowHTTP bool
	Timeout   time.Duration

	// Test seams. Transport replaces the network (the URL checks still run on every hop); Dial replaces the raw
	// socket dial of the pinned transport (the guard still validates the resolved addresses).
	Transport http.RoundTripper
	Dial      func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (f *Fetcher) guard() webtool.NetGuard {
	g := f.Guard
	if len(g.Ports) == 0 {
		g.Ports = Ports
	}
	return g
}

// transport is the only place that opens sockets: it resolves the name itself, validates ALL answers and dials
// the validated address (no second resolution to rebind), and the dialer re-checks the final address.
func (f *Fetcher) transport() http.RoundTripper {
	if f.Transport != nil {
		return f.Transport
	}
	t := &http.Transport{
		Proxy:                  nil, // never the environment proxy: it would bypass the guard
		DisableKeepAlives:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	if f.AllowPrivate {
		d := &net.Dialer{Timeout: 10 * time.Second}
		t.DialContext = d.DialContext
		if f.Dial != nil {
			t.DialContext = f.Dial
		}
		return t
	}
	g := f.guard()
	d := &net.Dialer{Timeout: 10 * time.Second, Control: g.ControlFunc()}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, webtool.ErrBadURL
		}
		addrs, err := g.Resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error = ferr(CodeFetch)
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
	}
	return t
}

// CheckURL validates a link before it is saved or fetched.
func (f *Fetcher) CheckURL(raw string) (*url.URL, error) {
	if f.AllowPrivate {
		return f.checkLenient(raw)
	}
	u, _, err := f.guard().CheckURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !f.AllowHTTP {
		return nil, webtool.ErrScheme
	}
	return u, nil
}

// checkLenient is CheckURL for installs that may reach their own network: syntax, scheme, no credentials.
func (f *Fetcher) checkLenient(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > webtool.MaxURLLength {
		return nil, webtool.ErrBadURL
	}
	for _, r := range raw {
		if r < 0x21 || r == 0x7f {
			return nil, webtool.ErrBadURL
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, webtool.ErrBadURL
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && f.AllowHTTP) {
		return nil, webtool.ErrScheme
	}
	if u.User != nil {
		return nil, webtool.ErrCredentials
	}
	u.Fragment = ""
	return u, nil
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

// Fetch downloads rawURL (GET only) and returns its bytes, capped at maxBytes. The auth header is sent to the
// link's own origin only: a redirect to another host never receives it.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, h *Header, maxBytes int64) (Download, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rt := f.transport()
	if t, ok := rt.(*http.Transport); ok {
		defer t.CloseIdleConnections()
	}
	origin, err := f.CheckURL(rawURL)
	if err != nil {
		return Download{}, err
	}
	cur := origin
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur.String(), nil)
		if err != nil {
			return Download{}, webtool.ErrBadURL
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/csv, application/json, application/vnd.openxmlformats-officedocument.spreadsheetml.sheet, application/pdf, text/plain;q=0.8, */*;q=0.1")
		if h != nil && h.Name != "" && sameOrigin(cur, origin) {
			req.Header.Set(h.Name, h.Value)
		}
		resp, err := rt.RoundTrip(req)
		if err != nil {
			if ctx.Err() != nil {
				return Download{}, ferr(CodeTimeout)
			}
			return Download{}, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if hop >= MaxRedirects {
				return Download{}, ferr(CodeTooManyRedirects)
			}
			next, err := cur.Parse(loc)
			if loc == "" || err != nil {
				return Download{}, ferr(CodeRedirect)
			}
			if cur.Scheme == "https" && next.Scheme == "http" && !f.AllowHTTP {
				return Download{}, ferr(CodeInsecureRedirect)
			}
			checked, err := f.CheckURL(next.String()) // every hop is validated again
			if err != nil {
				return Download{}, err
			}
			cur = checked
			continue
		}
		d, err := read(resp, cur, maxBytes)
		if err != nil {
			if ctx.Err() != nil {
				return Download{}, ferr(CodeTimeout)
			}
			return Download{}, err
		}
		d.FinalURL, d.Hops = cur.String(), hop
		return d, nil
	}
}

func read(resp *http.Response, u *url.URL, maxBytes int64) (Download, error) {
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return Download{}, &FetchError{Code: CodeHTTPStatus, Status: resp.StatusCode}
	}
	if resp.ContentLength > maxBytes {
		return Download{}, ferr(CodeTooLarge)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	mt = strings.ToLower(mt)
	if mt == "text/html" || mt == "application/xhtml+xml" {
		return Download{}, ferr(CodeWebPage) // a login or preview page, not the data: is the link published?
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return Download{}, err
	}
	if int64(len(data)) > maxBytes {
		return Download{}, ferr(CodeTooLarge)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Download{}, ferr(CodeEmpty)
	}
	ext := DetectExt(mt, dispositionName(resp.Header.Get("Content-Disposition")), u.Path, data)
	if ext == "" {
		return Download{}, ferr(CodeTypeNotAllowed)
	}
	return Download{Data: data, Ext: ext, Mime: mt}, nil
}

func dispositionName(cd string) string {
	if cd == "" {
		return ""
	}
	_, p, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return p["filename"]
}

func allowedExt(ext string) bool {
	for _, a := range AllowedExts {
		if a == ext {
			return true
		}
	}
	return false
}

var mimeExt = map[string]string{
	"text/csv": ".csv", "application/csv": ".csv", "text/comma-separated-values": ".csv",
	"text/tab-separated-values": ".tsv",
	"application/json":          ".json", "text/json": ".json",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx",
	"application/pdf": ".pdf",
	"text/plain":      ".txt",
}

// DetectExt decides the type of a download: the announced media type first, then the file name the server
// suggests, then the link's path, then the content's magic bytes. "" means the type is not allowed. The drive
// checks again that the content matches the extension before storing it.
func DetectExt(mediaType, dispName, urlPath string, data []byte) string {
	nameExt := ""
	for _, n := range []string{dispName, urlPath} {
		if e := strings.ToLower(path.Ext(n)); e != "" {
			nameExt = e
			break
		}
	}
	sniff := ""
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		sniff = ".pdf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		sniff = ".xlsx" // confirmed by the drive (the zip must hold xl/workbook.xml)
	}
	if e, ok := mimeExt[mediaType]; ok {
		// text/plain is generic: a .csv/.tsv/.json name is more precise.
		if e == ".txt" && (nameExt == ".csv" || nameExt == ".tsv" || nameExt == ".json") {
			return nameExt
		}
		return e
	}
	if strings.HasSuffix(mediaType, "+json") {
		return ".json"
	}
	generic := mediaType == "" || mediaType == "application/octet-stream" || mediaType == "binary/octet-stream" ||
		mediaType == "application/download" || mediaType == "application/force-download" || mediaType == "application/x-download"
	if !generic {
		return "" // an explicit type we do not import (images, html, zip, executables...)
	}
	if allowedExt(nameExt) {
		return nameExt
	}
	if nameExt == "" || nameExt == ".bin" {
		return sniff
	}
	return ""
}
