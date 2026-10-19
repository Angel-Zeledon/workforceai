package governance

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// PublicBackend is what the public API reads and writes; the host implements it with its own services, always
// under the organization bound to the context.
type PublicBackend interface {
	Agents(ctx context.Context) (any, error)
	Requests(ctx context.Context) (any, error)
	Request(ctx context.Context, id string) (any, error)
	Submit(ctx context.Context, text string, budgetCapUSD float64) (string, error)
	Reports(ctx context.Context) (any, error)
	Report(ctx context.Context, id string) (any, error)
}

type tokenCtxKey struct{}

// TokenFrom returns the API token that authenticated the request.
func TokenFrom(ctx context.Context) (APIToken, bool) {
	t, ok := ctx.Value(tokenCtxKey{}).(APIToken)
	return t, ok
}

// rateLimiter is a fixed-window counter per token: enough to stop a runaway integration, no external state.
type rateLimiter struct {
	mu     sync.Mutex
	win    map[string]*window
	limit  int
	window time.Duration
	now    func() time.Time
}

type window struct {
	start time.Time
	n     int
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.win[key]
	if w == nil || now.Sub(w.start) >= l.window {
		if len(l.win) > 10000 { // bounded memory
			l.win = map[string]*window{}
		}
		l.win[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.limit
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// PublicRouter serves the token-authenticated API (mounted at /api/public/v1). Every route checks its scope;
// the organization comes only from the token.
func (h *Handler) PublicRouter(b PublicBackend) http.Handler {
	rl := &rateLimiter{win: map[string]*window{}, limit: 120, window: time.Minute, now: h.now}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			org, t, err := h.Tokens.Authenticate(req.Context(), bearer(req))
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="aiwos"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid, expired or revoked token"})
				return
			}
			if !rl.allow(t.ID) {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit: 120 requests per minute per token"})
				return
			}
			ctx := context.WithValue(req.Context(), tokenCtxKey{}, t)
			ctx = h.Env.WithOrg(ctx, org, "token:"+t.ID)
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	need := func(scope string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				t, _ := TokenFrom(req.Context())
				if !HasScope(t.Scopes, scope) {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "the token lacks the scope " + scope})
					return
				}
				next.ServeHTTP(w, req)
			})
		}
	}
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 200, v)
	}
	r.With(need(ScopeAgentsRead)).Get("/agents", func(w http.ResponseWriter, req *http.Request) {
		v, err := b.Agents(req.Context())
		reply(w, v, err)
	})
	r.With(need(ScopeRequestsRead)).Get("/requests", func(w http.ResponseWriter, req *http.Request) {
		v, err := b.Requests(req.Context())
		reply(w, v, err)
	})
	r.With(need(ScopeRequestsRead)).Get("/requests/{id}", func(w http.ResponseWriter, req *http.Request) {
		v, err := b.Request(req.Context(), chi.URLParam(req, "id"))
		reply(w, v, err)
	})
	r.With(need(ScopeRequestsWrite)).Post("/requests", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Text         string  `json:"text"`
			BudgetCapUSD float64 `json:"budget_cap_usd"`
		}
		if err := h.Env.Decode(w, req, &body); err != nil {
			h.fail(w, err)
			return
		}
		id, err := b.Submit(req.Context(), body.Text, body.BudgetCapUSD)
		if err != nil {
			h.fail(w, err)
			return
		}
		t, _ := TokenFrom(req.Context())
		h.audit(req, "api.request_submitted", "request", id, map[string]any{"token_id": t.ID})
		writeJSON(w, http.StatusAccepted, map[string]string{"request_id": id})
	})
	r.With(need(ScopeReportsRead)).Get("/reports", func(w http.ResponseWriter, req *http.Request) {
		v, err := b.Reports(req.Context())
		reply(w, v, err)
	})
	r.With(need(ScopeReportsRead)).Get("/reports/{id}", func(w http.ResponseWriter, req *http.Request) {
		v, err := b.Report(req.Context(), chi.URLParam(req, "id"))
		reply(w, v, err)
	})
	r.With(need(ScopeUsageRead)).Get("/usage", func(w http.ResponseWriter, req *http.Request) {
		rep, err := h.buildUsage(req)
		reply(w, rep, err)
	})
	return r
}
