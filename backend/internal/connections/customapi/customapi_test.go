package customapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections"
)

func TestNormalizeRejectsUnsafeSources(t *testing.T) {
	bad := []connections.Source{
		{BaseURL: "ftp://erp.example.test"},
		{BaseURL: "https://user:pw@erp.example.test"},
		{BaseURL: "https://erp.example.test/?token=x"},
		{BaseURL: "erp.example.test"},
		{BaseURL: "https://erp.example.test", Auth: connections.SourceAuth{Style: "header", Name: "Host"}},
		{BaseURL: "https://erp.example.test", Auth: connections.SourceAuth{Style: "header"}},
		{BaseURL: "https://erp.example.test", Auth: connections.SourceAuth{Style: "cookie", Name: "x"}},
		{BaseURL: "https://erp.example.test", Endpoints: []connections.Endpoint{{Name: "a", Path: "/../admin"}}},
		{BaseURL: "https://erp.example.test", Endpoints: []connections.Endpoint{{Name: "a", Path: "//evil.test/x"}}},
		{BaseURL: "https://erp.example.test", Endpoints: []connections.Endpoint{{Name: "a", Path: "/x?y=1"}}},
		{BaseURL: "https://erp.example.test", Endpoints: []connections.Endpoint{{Name: "Bad Name", Path: "/x"}}},
		{BaseURL: "https://erp.example.test", Endpoints: []connections.Endpoint{{Name: "a", Path: "/x"}, {Name: "a", Path: "/y"}}},
	}
	for _, s := range bad {
		if _, err := s.Normalize(); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	in := connections.Source{BaseURL: " https://erp.example.test/api/ ", Endpoints: []connections.Endpoint{{Name: "stock", Path: "/p/{sku}/stock", Params: []string{"warehouse"}}}}
	got, err := in.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://erp.example.test/api" || got.Auth.Style != connections.AuthBearer {
		t.Fatalf("normalized %+v", got)
	}
	if p := got.Endpoints[0].Params; len(p) != 2 || p[1] != "sku" {
		t.Fatalf("placeholders become params: %v", p)
	}
	if len(in.Endpoints[0].Params) != 1 {
		t.Fatal("Normalize wrote into the caller's endpoints")
	}
}

// The default transport refuses private addresses, so an admin-written URL
// cannot reach the server's own network (the stub listens on 127.0.0.1).
func TestGuardedTransportRefusesPrivateAddresses(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	src := connections.Source{BaseURL: srv.URL, Auth: connections.SourceAuth{Style: connections.AuthBearer}}
	h := connections.Handle{Mode: connections.ModeLive, Source: &src, HTTP: &http.Client{Transport: Transport(false)}}
	p, _ := New()
	if res := p.Test(context.Background(), h); res.OK || res.Code != CodeBlocked || hit {
		t.Fatalf("loopback reachable: %+v hit=%v", res, hit)
	}
	h.HTTP = &http.Client{Transport: Transport(true)} // self-hosted on a trusted network
	if res := p.Test(context.Background(), h); !res.OK || !hit {
		t.Fatalf("allow-private: %+v hit=%v", res, hit)
	}
}

func TestItemsUnwrapsUsualListShapes(t *testing.T) {
	cases := map[string]int{
		`[{"a":1},{"a":2},{"a":3}]`:     3,
		`{"results":[{"a":1},{"a":2}]}`: 2,
		`{"total":2,"value":[{"a":1}]}`: 1,
		`{"name":"one object"}`:         1,
		`plain text answer`:             1,
		``:                              0,
		`{"data":[` + strings.Repeat(`{"a":1},`, 40) + `{"a":1}]}`: 10,
	}
	for body, want := range cases {
		items, _ := Items([]byte(body), "application/json", 10)
		if len(items) != want {
			t.Errorf("%.40s: %d items, want %d", body, len(items), want)
		}
	}
	items, total := Items([]byte(`{"data":[`+strings.Repeat(`{"a":1},`, 40)+`{"a":1}]}`), "", 10)
	if total != 41 || len(items) != 10 {
		t.Fatalf("total %d items %d", total, len(items))
	}
	if items, _ := Items([]byte("<p>hi</p>"), "text/html; charset=utf-8", 5); !items[0].Texts[0].HTML {
		t.Fatal("an HTML body must be marked so the sanitizer strips it")
	}
}

func TestTestAcceptsReachableRootButNotARejectedKey(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	src := connections.Source{BaseURL: srv.URL}
	h := connections.Handle{Mode: connections.ModeLive, Source: &src, HTTP: srv.Client()}
	p, _ := New()
	if res := p.Test(context.Background(), h); !res.OK {
		t.Fatalf("404 at the root means reachable: %+v", res)
	}
	status = http.StatusUnauthorized
	if res := p.Test(context.Background(), h); res.OK || res.Code != connections.CodeReauthRequired {
		t.Fatalf("rejected key: %+v", res)
	}
	if res := p.Test(context.Background(), connections.Handle{Mode: connections.ModeLive}); res.OK {
		t.Fatal("a source without configuration cannot pass a test")
	}
}
