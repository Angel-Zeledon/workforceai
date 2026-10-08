package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Shared helpers for provider adapters (Calendar, Drive, GitHub, Slack). Gmail
// keeps its own copies; new adapters use these so behavior stays uniform.

// MaxResponseBytes caps what an adapter reads from a provider response.
const MaxResponseBytes = 4 << 20

// StatusCode maps an HTTP status to a stable, body-free error code.
func StatusCode(st int) string {
	switch {
	case st == http.StatusUnauthorized:
		return CodeReauthRequired
	case st == http.StatusForbidden:
		return "insufficient_scope"
	case st == http.StatusNotFound:
		return "not_found"
	case st == http.StatusTooManyRequests:
		return "rate_limited"
	}
	return CodeProviderError
}

// RetryAfter reads a Retry-After header in seconds.
func RetryAfter(h http.Header) time.Duration {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// DoJSON performs an authenticated JSON call through the connection handle
// (whose transport injects the token) and maps failures to classified errors
// that never carry the response body. It returns the status and bytes moved.
func DoJSON(ctx context.Context, h Handle, method, rawURL string, q url.Values, body any, out any) (int, int64, error) {
	if h.HTTP == nil {
		return 0, 0, &ProviderError{Code: CodeConnectionUnavailable}
	}
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	var rd io.Reader
	var sent int64
	if body != nil {
		b, _ := json.Marshal(body)
		sent = int64(len(b))
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, 0, &ProviderError{Code: CodeInvalidArgs}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		return 0, 0, Classify(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, int64(len(raw)), &ProviderError{Status: resp.StatusCode, Code: StatusCode(resp.StatusCode), RetryAfter: RetryAfter(resp.Header)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, int64(len(raw)), &ProviderError{Status: resp.StatusCode, Code: CodeProviderError}
		}
	}
	return resp.StatusCode, int64(len(raw)) + sent, nil
}

// ArgStr returns the first non-empty argument among keys, trimmed.
func ArgStr(a map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := a[k]; ok && v != nil {
			s, ok := v.(string)
			if !ok {
				s = fmt.Sprint(v)
			}
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

// ArgInt returns an integer argument (JSON numbers arrive as float64).
func ArgInt(a map[string]any, def int, keys ...string) int {
	for _, k := range keys {
		switch v := a[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return def
}

// ArgList returns a list argument given as an array or a comma separated string.
func ArgList(a map[string]any, key string) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	switch v := a[key].(type) {
	case string:
		for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
			add(p)
		}
	case []any:
		for _, x := range v {
			add(fmt.Sprint(x))
		}
	case []string:
		for _, x := range v {
			add(x)
		}
	}
	return out
}

// Limit clamps a requested item count to [1, maxItems] (maxItems <= 0: no cap).
func Limit(a map[string]any, def, maxItems int) int {
	n := ArgInt(a, def, "max_results", "limit")
	if n < 1 {
		n = 1
	}
	if maxItems > 0 && n > maxItems {
		n = maxItems
	}
	return n
}

// InList reports whether v is allowed by a resource filter. An empty filter
// allows everything (the connection was created without narrowing).
func InList(filter []string, v string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if strings.EqualFold(strings.TrimSpace(f), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// OutOfScope is the error of a call outside the connection's resource filter.
func OutOfScope() error { return &ProviderError{Code: CodeOutOfScope} }

// InvalidArgs is the error of a call with missing or malformed arguments.
func InvalidArgs() error { return &ProviderError{Code: CodeInvalidArgs} }

// Truncate cuts s to n runes.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// StaticToken is the "refresh" of API-key providers (fine-grained GitHub
// tokens, Slack bot tokens): the vaulted secret is the bearer token itself. It
// is held in memory for a short time only, like an OAuth access token.
func StaticToken(secret string) (AccessToken, error) {
	if strings.TrimSpace(secret) == "" {
		return AccessToken{}, &ProviderError{Code: CodeReauthRequired}
	}
	return AccessToken{Value: secret, Expiry: time.Now().Add(10 * time.Minute)}, nil
}
