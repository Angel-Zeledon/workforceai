package linkimports

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"aiworkforce/backend/internal/webtool"
)

// fakeResolver answers fixed addresses per host.
type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

// rtFunc is an http.RoundTripper made of a function.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, ct string, body []byte, hdr ...string) *http.Response {
	h := http.Header{}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		h.Set(hdr[i], hdr[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func code(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	c, _ := CodeOf(err)
	return c
}

func TestFetchRefusesPrivateAddressesBeforeAnyConnection(t *testing.T) {
	dialed := false
	f := &Fetcher{
		Guard: webtool.NetGuard{Resolver: fakeResolver{
			"data.example.com":  {netip.MustParseAddr("10.0.0.5")},
			"mixed.example.com": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
			"meta.example.com":  {netip.MustParseAddr("169.254.169.254")},
		}},
		Dial: func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, errors.New("must not dial") },
	}
	for _, u := range []string{
		"https://127.0.0.1/x.csv", "https://10.1.2.3/x.csv", "https://[::1]/x.csv", "https://169.254.169.254/latest/meta-data",
		"https://[::ffff:127.0.0.1]/x", "https://2130706433/x", "https://localhost/x.csv", "https://printer.local/x.csv",
	} {
		if _, err := f.Fetch(context.Background(), u, nil, 1<<20); err == nil {
			t.Fatalf("%s: a private or internal address must be refused", u)
		}
	}
	for _, u := range []string{"https://data.example.com/x.csv", "https://mixed.example.com/x.csv", "https://meta.example.com/x"} {
		if c := code(t, func() error { _, err := f.Fetch(context.Background(), u, nil, 1<<20); return err }()); c != "address_not_allowed" {
			t.Fatalf("%s: a name resolving to a private address: %s", u, c)
		}
	}
	if dialed {
		t.Fatal("no socket may be opened to a private address")
	}
}

func TestFetchSchemeAndPortRules(t *testing.T) {
	f := &Fetcher{Transport: rtFunc(func(*http.Request) (*http.Response, error) { return resp(200, "text/csv", []byte("a,b\n1,2\n")), nil })}
	for u, want := range map[string]string{
		"http://example.com/x.csv":       "url_scheme_not_allowed",
		"ftp://example.com/x.csv":        "url_scheme_not_allowed",
		"file:///etc/passwd":             "url_scheme_not_allowed",
		"https://user:pw@example.com/x":  "url_credentials_not_allowed",
		"https://example.com:2375/x.csv": "url_port_not_allowed",
		"https://example.com:22/x.csv":   "url_port_not_allowed",
	} {
		_, err := f.Fetch(context.Background(), u, nil, 1<<20)
		if c := code(t, err); c != want {
			t.Fatalf("%s: got %s want %s", u, c, want)
		}
	}
	if _, err := f.Fetch(context.Background(), "https://example.com:8443/x.csv", nil, 1<<20); err != nil {
		t.Fatalf("8443 is an allowed port: %v", err)
	}
	f.AllowHTTP = true
	if _, err := f.Fetch(context.Background(), "http://example.com/x.csv", nil, 1<<20); err != nil {
		t.Fatalf("http allowed by LINK_IMPORTS_ALLOW_HTTP: %v", err)
	}
}

func TestFetchRedirectsAreCheckedAgain(t *testing.T) {
	for target, want := range map[string]string{
		"http://169.254.169.254/latest/meta-data": "insecure_redirect",
		"https://169.254.169.254/latest":          "address_not_allowed",
		"https://127.0.0.1:443/admin":             "address_not_allowed",
		"https://intranet.corp/report.csv":        "host_not_allowed",
		"file:///etc/passwd":                      "url_scheme_not_allowed",
	} {
		calls := 0
		f := &Fetcher{
			Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "public.example.com" {
					t.Fatalf("the redirect target %s must never be requested", r.URL)
				}
				return resp(302, "", nil, "Location", target), nil
			}),
		}
		_, err := f.Fetch(context.Background(), "https://public.example.com/export.csv", nil, 1<<20)
		if c := code(t, err); c != want {
			t.Fatalf("redirect to %s: got %s want %s", target, c, want)
		}
		if calls != 1 {
			t.Fatalf("one request only, got %d", calls)
		}
	}
	// A real (pinned) transport: the redirect target resolves to a private address and is never dialed.
	dials := []string{}
	f := &Fetcher{
		Guard: webtool.NetGuard{Resolver: fakeResolver{"public.example.com": {netip.MustParseAddr("93.184.216.34")},
			"private.example.com": {netip.MustParseAddr("10.9.9.9")}}},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dials = append(dials, addr)
			return nil, errors.New("offline test")
		},
	}
	_, err := f.Fetch(context.Background(), "https://private.example.com/x.csv", nil, 1<<20)
	if c := code(t, err); c != "address_not_allowed" {
		t.Fatalf("pinned transport: %s", c)
	}
	_, _ = f.Fetch(context.Background(), "https://public.example.com/x.csv", nil, 1<<20)
	if len(dials) != 1 || dials[0] != "93.184.216.34:443" {
		t.Fatalf("only the validated public address is dialed: %v", dials)
	}
}

// End to end through the real pinned transport: the server at the public address answers with a redirect to a
// name that resolves to a private address; that address is never dialed.
func TestFetchRedirectToAPrivateNameThroughThePinnedTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://private.example.com/x.csv", http.StatusFound)
	}))
	defer srv.Close()
	var dials []string
	f := &Fetcher{AllowHTTP: true,
		Guard: webtool.NetGuard{Resolver: fakeResolver{"public.example.com": {netip.MustParseAddr("93.184.216.34")},
			"private.example.com": {netip.MustParseAddr("10.9.9.9")}}},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials = append(dials, addr)
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
	_, err := f.Fetch(context.Background(), "http://public.example.com/export.csv", nil, 1<<20)
	if c := code(t, err); c != "address_not_allowed" {
		t.Fatalf("got %s (%v)", c, err)
	}
	if len(dials) != 1 || dials[0] != "93.184.216.34:80" {
		t.Fatalf("only the public address is dialed: %v", dials)
	}
}

func TestFetchTooManyRedirects(t *testing.T) {
	f := &Fetcher{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return resp(301, "", nil, "Location", "/again"), nil
	})}
	_, err := f.Fetch(context.Background(), "https://example.com/x.csv", nil, 1<<20)
	if c := code(t, err); c != CodeTooManyRedirects {
		t.Fatal(c)
	}
}

func TestFetchHeaderOnlyToTheLinkOrigin(t *testing.T) {
	seen := map[string]string{}
	f := &Fetcher{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Host] = r.Header.Get("X-Api-Key")
		switch r.URL.Host {
		case "api.example.com":
			if r.URL.Path == "/export" {
				return resp(302, "", nil, "Location", "/export.csv"), nil
			}
			return resp(302, "", nil, "Location", "https://cdn.example.net/file.csv"), nil
		}
		return resp(200, "text/csv", []byte("a,b\n1,2\n")), nil
	})}
	d, err := f.Fetch(context.Background(), "https://api.example.com/export", &Header{Name: "X-Api-Key", Value: "secret-1234"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if seen["api.example.com"] != "secret-1234" {
		t.Fatal("the header goes to the link's origin")
	}
	if seen["cdn.example.net"] != "" {
		t.Fatal("the header must never follow a redirect to another host")
	}
	if d.Ext != ".csv" || d.Hops != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestFetchSizeAndTypeLimits(t *testing.T) {
	big := bytes.Repeat([]byte("a,b\n"), 1000)
	cases := []struct {
		name string
		r    *http.Response
		url  string
		want string
	}{
		{"declared too large", resp(200, "text/csv", big), "https://x.example.com/a.csv", CodeTooLarge},
		{"streamed too large", func() *http.Response { r := resp(200, "text/csv", big); r.ContentLength = -1; return r }(), "https://x.example.com/a.csv", CodeTooLarge},
		{"html page", resp(200, "text/html; charset=utf-8", []byte("<html>login</html>")), "https://x.example.com/a.csv", CodeWebPage},
		{"image", resp(200, "image/png", []byte("\x89PNG\r\n\x1a\nxxxx")), "https://x.example.com/a.png", CodeTypeNotAllowed},
		{"zip", resp(200, "application/zip", []byte("PK\x03\x04xxxx")), "https://x.example.com/a.zip", CodeTypeNotAllowed},
		{"exe by name", resp(200, "application/octet-stream", []byte("MZxxxx")), "https://x.example.com/setup.exe", CodeTypeNotAllowed},
		{"legacy xls", resp(200, "application/vnd.ms-excel", []byte("\xd0\xcf\x11\xe0xxxx")), "https://x.example.com/a.xls", CodeTypeNotAllowed},
		{"empty", resp(200, "text/csv", []byte("  \n")), "https://x.example.com/a.csv", CodeEmpty},
		{"404", resp(404, "text/plain", []byte("nope")), "https://x.example.com/a.csv", CodeHTTPStatus},
	}
	for _, c := range cases {
		f := &Fetcher{Transport: rtFunc(func(*http.Request) (*http.Response, error) { return c.r, nil })}
		_, err := f.Fetch(context.Background(), c.url, nil, 1000)
		got, st := CodeOf(err)
		if got != c.want {
			t.Fatalf("%s: got %s (%v) want %s", c.name, got, err, c.want)
		}
		if c.want == CodeHTTPStatus && st != 404 {
			t.Fatalf("status %d", st)
		}
	}
}

func TestDetectExt(t *testing.T) {
	for _, c := range []struct{ mt, disp, path, data, want string }{
		{"text/csv", "", "/pub", "a,b", ".csv"},
		{"text/plain", "", "/export.tsv", "a\tb", ".tsv"},
		{"text/plain", "", "/notes", "hello", ".txt"},
		{"application/json", "", "/api/v1/orders", "[]", ".json"},
		{"application/vnd.api+json", "", "/x", "{}", ".json"},
		{"application/octet-stream", "report.xlsx", "/dl", "PK\x03\x04", ".xlsx"},
		{"application/octet-stream", "", "/dl", "%PDF-1.7", ".pdf"},
		{"", "", "/files/data.json", "{}", ".json"},
		{"application/pdf", "", "/x", "%PDF-", ".pdf"},
		{"text/html", "", "/x.csv", "<html>", ""},
		{"application/octet-stream", "", "/x.exe", "MZ", ""},
	} {
		if got := DetectExt(c.mt, c.disp, c.path, []byte(c.data)); got != c.want {
			t.Fatalf("%+v: got %q", c, got)
		}
	}
}

func TestAllowPrivateReachesTheLANButKeepsTheOtherRules(t *testing.T) {
	f := &Fetcher{AllowPrivate: true, Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		return resp(200, "application/json", []byte(`{"ok":true}`)), nil
	})}
	if _, err := f.Fetch(context.Background(), "https://10.0.0.5/export.json", nil, 1<<20); err != nil {
		t.Fatalf("CUSTOM_API_ALLOW_PRIVATE lets an install reach its LAN: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "http://10.0.0.5/export.json", nil, 1<<20); !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("https stays the default: %v", err)
	}
}
