package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Principal is the authenticated caller: user, active organization and role.
type Principal struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Role   Role   `json:"role"`
}

// Can reports whether the principal's role grants perm.
func (p Principal) Can(perm Permission) bool { return p.Role.Has(perm) }

type ctxKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFrom returns the authenticated principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok && p.UserID != "" && p.OrgID != ""
}

// OrgIDFrom returns the organization id of the authenticated principal.
func OrgIDFrom(ctx context.Context) (string, bool) {
	p, ok := PrincipalFrom(ctx)
	return p.OrgID, ok
}

// UserIDFrom returns the user id of the authenticated principal.
func UserIDFrom(ctx context.Context) (string, bool) {
	p, ok := PrincipalFrom(ctx)
	return p.UserID, ok
}

// Authenticator is a chi/net-http middleware that requires a valid
// `Authorization: Bearer <access token>` header and puts the Principal in the
// request context. Invalid, expired or revoked credentials get a 401.
func (s *Service) Authenticator() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerToken(r)
			if !ok {
				unauthorized(w, "missing bearer token", "")
				return
			}
			p, err := s.Authenticate(r.Context(), tok)
			if err != nil {
				switch {
				case errors.Is(err, ErrTokenExpired):
					unauthorized(w, "token expired", "invalid_token")
				case errors.Is(err, ErrInvalidToken), errors.Is(err, ErrUnauthenticated):
					unauthorized(w, "invalid token", "invalid_token")
				default:
					writeError(w, http.StatusInternalServerError, "internal", "internal error")
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	t := strings.TrimSpace(h[len(prefix):])
	return t, t != ""
}

// RequirePermission allows the request only if the principal holds ALL perms.
// Must run after Authenticator; fails closed (401) without a principal.
func RequirePermission(perms ...Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				unauthorized(w, "authentication required", "")
				return
			}
			for _, perm := range perms {
				if !p.Can(perm) {
					writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole allows the request only for roles ranking at least min.
func RequireRole(min Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				unauthorized(w, "authentication required", "")
				return
			}
			if !min.Valid() || p.Role.Rank() < min.Rank() {
				writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyFunc derives the rate-limit key of a request.
type KeyFunc func(r *http.Request) string

// RateLimit throttles requests with limiter. When the limiter backend fails,
// the request is allowed if failOpen, otherwise rejected with 503.
func RateLimit(limiter Limiter, limit Limit, key KeyFunc, failOpen bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := limiter.Allow(r.Context(), key(r), limit)
			if err != nil {
				if failOpen {
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, http.StatusServiceUnavailable, "unavailable", "rate limiter unavailable")
				return
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Max))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			if !res.Allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter/time.Second)+1))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPKey keys requests by client IP. X-Forwarded-For is honoured only when the
// direct peer is inside trustedProxies (then the right-most untrusted hop is
// used), so clients cannot spoof their address.
func IPKey(prefix string, trustedProxies []netip.Prefix) KeyFunc {
	return func(r *http.Request) string { return prefix + ":ip:" + ClientIP(r, trustedProxies) }
}

// UserKey keys requests by authenticated user (falls back to the client IP).
// Use after Authenticator.
func UserKey(prefix string, trustedProxies []netip.Prefix) KeyFunc {
	return func(r *http.Request) string {
		if p, ok := PrincipalFrom(r.Context()); ok {
			return prefix + ":user:" + p.UserID
		}
		return prefix + ":ip:" + ClientIP(r, trustedProxies)
	}
}

// ClientIP returns the caller's IP address; see IPKey.
func ClientIP(r *http.Request, trustedProxies []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !inPrefixes(peer, trustedProxies) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // garbage entry: stop trusting the chain
		}
		a = a.Unmap()
		if !inPrefixes(a, trustedProxies) {
			return a.String()
		}
	}
	return peer.String()
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ---- JSON error helpers ----

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

func unauthorized(w http.ResponseWriter, msg, bearerErr string) {
	h := `Bearer realm="api"`
	if bearerErr != "" {
		h += `, error="` + bearerErr + `"`
	}
	w.Header().Set("WWW-Authenticate", h)
	writeError(w, http.StatusUnauthorized, "unauthorized", msg)
}
