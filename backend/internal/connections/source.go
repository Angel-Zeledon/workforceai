package connections

import (
	"net/url"
	"regexp"
	"strings"
)

// Source configures a generic read-only HTTP source (provider custom_api): any
// platform with an HTTP API can be queried by agents without writing an
// adapter. It is organization configuration written by an admin, never by an
// agent; the API key itself lives in the vault like every other credential.
type Source struct {
	BaseURL     string     `json:"base_url"`
	Auth        SourceAuth `json:"auth"`
	Description string     `json:"description,omitempty"`
	// Endpoints narrow what agents may call; empty = any GET under BaseURL.
	Endpoints []Endpoint `json:"endpoints,omitempty"`
}

// SourceAuth says how the API key travels: "bearer" (Authorization: Bearer),
// "header" (a named header) or "query" (a named query parameter).
type SourceAuth struct {
	Style string `json:"style"`
	Name  string `json:"name,omitempty"`
}

// Endpoint is one named read query of a source. Path may hold {param}
// placeholders; Params lists every argument the agent may pass (placeholders
// and query parameters).
type Endpoint struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description,omitempty"`
	Params      []string `json:"params,omitempty"`
}

// Auth styles of a Source.
const (
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthQuery  = "query"
)

const (
	maxSourceDescription   = 500
	maxEndpointDescription = 300
	maxEndpoints           = 30
	maxEndpointParams      = 20
)

var (
	endpointNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	paramNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	// Header or query parameter that carries the key (RFC 7230 token subset).
	authNameRe    = regexp.MustCompile(`^[A-Za-z0-9_.~-]{1,100}$`)
	placeholderRe = regexp.MustCompile(`\{([^{}]*)\}`)
	// Headers an API key must never be written into: they steer the request itself.
	reservedHeaders = map[string]bool{"host": true, "content-length": true, "transfer-encoding": true, "connection": true,
		"cookie": true, "proxy-authorization": true, "te": true, "upgrade": true}
)

// Normalize validates a source and returns it trimmed, with defaults applied.
// It checks the syntax only: where the URL may resolve to is the transport's
// job (the network guard runs on every dial).
func (s Source) Normalize() (Source, error) {
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	u, err := url.Parse(s.BaseURL)
	switch {
	case s.BaseURL == "" || err != nil || u.Host == "" || u.Opaque != "":
		return Source{}, invalid("source.base_url must be an absolute http(s) URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return Source{}, invalid("source.base_url must use http or https")
	case u.User != nil:
		return Source{}, invalid("source.base_url must not carry credentials; use the API key")
	case u.RawQuery != "" || u.Fragment != "":
		return Source{}, invalid("source.base_url must not have a query or fragment")
	case len(s.BaseURL) > 500:
		return Source{}, invalid("source.base_url is too long")
	}
	s.Auth.Style = strings.ToLower(strings.TrimSpace(s.Auth.Style))
	s.Auth.Name = strings.TrimSpace(s.Auth.Name)
	switch s.Auth.Style {
	case "", AuthBearer:
		s.Auth = SourceAuth{Style: AuthBearer}
	case AuthHeader:
		if !authNameRe.MatchString(s.Auth.Name) || reservedHeaders[strings.ToLower(s.Auth.Name)] {
			return Source{}, invalid("source.auth.name must be a valid header name")
		}
	case AuthQuery:
		if !authNameRe.MatchString(s.Auth.Name) {
			return Source{}, invalid("source.auth.name must be a valid query parameter name")
		}
	default:
		return Source{}, invalid("source.auth.style must be bearer, header or query")
	}
	s.Description = strings.TrimSpace(s.Description)
	if len([]rune(s.Description)) > maxSourceDescription {
		return Source{}, invalid("source.description is too long (max %d)", maxSourceDescription)
	}
	if len(s.Endpoints) > maxEndpoints {
		return Source{}, invalid("a source has at most %d endpoints", maxEndpoints)
	}
	s.Endpoints = append([]Endpoint(nil), s.Endpoints...) // never write into the caller's slice
	seen := map[string]bool{}
	for i, e := range s.Endpoints {
		e.Name = strings.TrimSpace(e.Name)
		e.Path = strings.TrimSpace(e.Path)
		e.Description = strings.TrimSpace(e.Description)
		if !endpointNameRe.MatchString(e.Name) {
			return Source{}, invalid("endpoint name %q must be lowercase letters, digits or _", e.Name)
		}
		if seen[e.Name] {
			return Source{}, invalid("endpoint %q is repeated", e.Name)
		}
		seen[e.Name] = true
		if err := CheckRelPath(e.Path); err != nil {
			return Source{}, invalid("endpoint %q: %s", e.Name, err.Error())
		}
		if len([]rune(e.Description)) > maxEndpointDescription {
			return Source{}, invalid("endpoint %q: description is too long (max %d)", e.Name, maxEndpointDescription)
		}
		params := dedupe(e.Params)
		for _, m := range placeholderRe.FindAllStringSubmatch(e.Path, -1) {
			params = dedupe(append(params, m[1])) // every placeholder is a parameter
		}
		if len(params) > maxEndpointParams {
			return Source{}, invalid("endpoint %q has too many params", e.Name)
		}
		for _, p := range params {
			if !paramNameRe.MatchString(p) {
				return Source{}, invalid("endpoint %q: invalid param %q", e.Name, p)
			}
		}
		e.Params = params
		s.Endpoints[i] = e
	}
	return s, nil
}

// CheckRelPath accepts a path relative to the base URL: it starts with "/",
// cannot climb out of the base ("..") nor name another host, and carries no
// query (parameters travel separately).
func CheckRelPath(p string) error {
	switch {
	case !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//"):
		return errStr("path must start with a single /")
	case len(p) > 300:
		return errStr("path is too long")
	case strings.ContainsAny(p, "?#\\ \t\r\n"):
		return errStr("path must not contain a query, fragment, spaces or backslashes")
	case strings.Contains(p, "://"):
		return errStr("path must be relative to the base URL")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return errStr("path must not contain . or .. segments")
		}
	}
	return nil
}

type errStr string

func (e errStr) Error() string { return string(e) }
