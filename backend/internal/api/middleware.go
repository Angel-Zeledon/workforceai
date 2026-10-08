package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// ---- CORS / origins ----

// originPolicy decides which browser origins may call the API and open the
// WebSocket. The same policy is used for CORS and for the WS handshake.
//
//   - ALLOWED_ORIGINS set: only those exact origins are accepted (reflected
//     back in Access-Control-Allow-Origin, with Vary: Origin).
//   - empty and auth disabled (dev/demo): any origin ("*").
//   - empty and auth enabled: no cross-origin access at all (same-origin only).
type originPolicy struct {
	any  bool
	list map[string]bool
}

func newOriginPolicy(list []string, authEnabled bool) originPolicy {
	p := originPolicy{list: map[string]bool{}}
	for _, o := range list {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		switch {
		case o == "":
		case o == "*":
			p.any = true
		default:
			p.list[strings.ToLower(o)] = true
		}
	}
	if len(p.list) == 0 && !p.any && !authEnabled {
		p.any = true
	}
	return p
}

func (p originPolicy) allowed(origin string) bool {
	if p.any {
		return true
	}
	return p.list[strings.ToLower(strings.TrimRight(origin, "/"))]
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && s.origins.allowed(origin) {
			if s.origins.any {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkOrigin is the WebSocket CheckOrigin. Requests without Origin (non
// browser clients) pass the origin check; they still need a token when auth is
// on. A browser's same-origin request is always accepted.
func (s *server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || s.origins.allowed(origin) {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// ---- authentication / tenancy ----

// wsToken lets browsers (which cannot set headers on a WebSocket) pass the
// access token as ?access_token=. It only applies to /ws and never overrides
// an Authorization header. Access tokens are short-lived (15 min by default);
// make sure proxies do not log query strings.
func (s *server) wsToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AuthEnabled && r.Header.Get("Authorization") == "" {
			if tok := r.URL.Query().Get("access_token"); tok != "" {
				r = r.Clone(r.Context())
				r.Header.Set("Authorization", "Bearer "+tok)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate requires a valid bearer token when auth is enabled.
func (s *server) authenticate(next http.Handler) http.Handler {
	if !s.AuthEnabled {
		return next
	}
	return s.Auth.Authenticator()(next)
}

// tenant binds the request context to its organization (and actor). With auth
// the org is the one in the verified token, so a client can never choose it;
// without auth it is the fixed demo org. It also makes sure a newly registered
// organization has its agent roster.
func (s *server) tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		org := s.Cfg.OrgID
		if s.AuthEnabled {
			p, ok := auth.PrincipalFrom(ctx)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			org = p.OrgID
			ctx = application.WithActor(ctx, p.UserID)
			ctx = application.WithActorRole(ctx, string(p.Role))
		}
		ctx = application.WithOrg(ctx, org)
		if err := s.ensureSeeded(ctx, org); err != nil {
			s.fail(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ensureSeeded creates the default agents of an organization that has none
// (first request after registration). Idempotent; verified once per process.
func (s *server) ensureSeeded(ctx context.Context, org string) error {
	if !s.AuthEnabled || org == s.Cfg.OrgID {
		return nil // the demo org is seeded at startup
	}
	if _, ok := s.seeded.Load(org); ok {
		return nil
	}
	agents, err := s.Store.ListAgents(ctx, org)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		o := domain.Organization{ID: org, Name: org, Slug: org, BudgetUSD: s.Cfg.BudgetUSD}
		if err := s.Store.Seed(ctx, o, roles.SeedAgents()); err != nil {
			return err
		}
	}
	s.seeded.Store(org, struct{}{})
	return nil
}

func passthrough(next http.Handler) http.Handler { return next }

// can requires a permission when auth is enabled (no-op otherwise).
func (s *server) can(p auth.Permission) func(http.Handler) http.Handler {
	if !s.AuthEnabled {
		return passthrough
	}
	return auth.RequirePermission(p)
}

// requireRole requires at least the given role when auth is enabled.
func (s *server) requireRole(min auth.Role) func(http.Handler) http.Handler {
	if !s.AuthEnabled {
		return passthrough
	}
	return auth.RequireRole(min)
}
