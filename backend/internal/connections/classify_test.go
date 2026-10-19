package connections

import (
	"net/http"
	"testing"
)

func TestClassifyResponse(t *testing.T) {
	g := []byte(`{"error":{"errors":[{"reason":"userRateLimitExceeded"}]}}`)
	cases := []struct {
		st   int
		h    http.Header
		body []byte
		want string
	}{
		{403, http.Header{}, g, "rate_limited"},
		{403, http.Header{"X-Ratelimit-Remaining": {"0"}}, nil, "rate_limited"},
		{403, http.Header{}, []byte(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`), "rate_limited"},
		{403, http.Header{}, []byte(`{"error":{"errors":[{"reason":"forbidden"}]}}`), StatusCode(403)},
		{401, http.Header{}, g, StatusCode(401)},
	}
	for i, c := range cases {
		if got := ClassifyResponse(c.st, c.h, c.body); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestNormalizeScope(t *testing.T) {
	if NormalizeScope("https://www.googleapis.com/auth/userinfo.email") != "email" || NormalizeScope("x") != "x" {
		t.Fatal("bad normalization")
	}
}
