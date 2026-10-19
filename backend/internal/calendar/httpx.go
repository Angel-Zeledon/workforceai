package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aiworkforce/backend/internal/connections"
)

const maxBody = 4 << 20

// doJSON performs a JSON call with the connection's HTTP client (its transport
// injects the bearer token). Failures map to the package errors; the provider
// body is never surfaced.
func doJSON(ctx context.Context, hc *http.Client, method, rawURL string, q url.Values, hdr map[string]string, body, out any) (int, error) {
	if hc == nil {
		return 0, ErrNotConfigured
	}
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if pe := connections.Classify(err); pe.Code == connections.CodeReauthRequired {
			return 0, ErrReauth
		}
		return 0, ErrProvider
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode/100 == 2:
	case resp.StatusCode == http.StatusUnauthorized:
		return resp.StatusCode, ErrReauth
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return resp.StatusCode, ErrNotFound
	case resp.StatusCode == http.StatusBadRequest:
		return resp.StatusCode, ErrInvalid
	default:
		return resp.StatusCode, ErrProvider
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, ErrProvider
		}
	}
	return resp.StatusCode, nil
}

func cleanAddrs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" || !strings.Contains(a, "@") || seen[strings.ToLower(a)] {
			continue
		}
		seen[strings.ToLower(a)] = true
		out = append(out, a)
	}
	return out
}
