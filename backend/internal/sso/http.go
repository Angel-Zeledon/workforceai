package sso

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

func sortDomains(d []Domain) { sort.Slice(d, func(i, j int) bool { return d[i].Domain < d[j].Domain }) }

// HandlerConfig configures the HTTP layer.
type HandlerConfig struct {
	Limiter        auth.Limiter
	TrustedProxies []netip.Prefix
	FailOpen       bool
	// Auth authenticates the admin routes (bearer access token).
	Auth *auth.Service
	// Extra admin routes (e.g. SCIM token management) mounted on the same authenticated router.
	Extra func(r chi.Router)
}

// Handler exposes the module over HTTP.
type Handler struct {
	svc *Service
	cfg HandlerConfig
}

// NewHandler builds the HTTP layer.
func NewHandler(svc *Service, cfg HandlerConfig) *Handler { return &Handler{svc: svc, cfg: cfg} }

func (h *Handler) lim(prefix string, max int, window time.Duration) func(http.Handler) http.Handler {
	if h.cfg.Limiter == nil {
		return func(n http.Handler) http.Handler { return n }
	}
	return auth.RateLimit(h.cfg.Limiter, auth.Limit{Max: max, Window: window}, auth.IPKey(prefix, h.cfg.TrustedProxies), h.cfg.FailOpen)
}

// PublicRoutes (mounted at /api/v1/sso) is what a browser uses before it has a session.
//
//	POST /discover  {email}            -> {sso: bool}
//	POST /start     {email}            -> {url}      (the provider's authorization URL)
//	GET  /callback  ?code=&state=      -> 302 to the login page with ?sso_code= (or ?sso_error=)
func (h *Handler) PublicRoutes() http.Handler {
	r := chi.NewRouter()
	r.With(h.lim("sso-discover", 30, 15*time.Minute)).Post("/discover", h.discover)
	r.With(h.lim("sso-start", 20, 15*time.Minute)).Post("/start", h.start)
	r.With(h.lim("sso-callback", 60, 15*time.Minute)).Get("/callback", h.callback)
	return r
}

// AdminRoutes (mounted at /api/v1/identity) is the Settings -> Security API. Every route needs org:manage.
func (h *Handler) AdminRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.cfg.Auth.Authenticator(), auth.RequirePermission(auth.PermOrgManage))
	r.Get("/sso", h.getConfig)
	r.Put("/sso", h.putConfig)
	r.Delete("/sso", h.deleteConfig)
	r.Post("/sso/test", h.testConfig)
	r.Get("/domains", h.listDomains)
	r.Post("/domains", h.addDomain)
	r.Post("/domains/{domain}/verify", h.verifyDomain)
	r.Delete("/domains/{domain}", h.removeDomain)
	r.Get("/policy", h.getPolicy)
	r.Put("/policy", h.putPolicy)
	if h.cfg.Extra != nil {
		h.cfg.Extra(r)
	}
	return r
}

type errBody struct {
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

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	var b errBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", "unexpected trailing data")
		return false
	}
	return true
}

func (h *Handler) discover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sso": h.svc.Available(r.Context(), in.Email)})
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := h.svc.Start(r.Context(), in.Email)
	if errors.Is(err, ErrNoSSO) {
		writeErr(w, http.StatusNotFound, "sso_unavailable", "single sign-on is not available for this address")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sso_provider", "the identity provider could not be reached")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := func(k, v string) {
		u, err := url.Parse(strings.TrimRight(h.svc.o.AppURL, "/") + "/login")
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		qq := u.Query()
		qq.Set(k, v)
		u.RawQuery = qq.Encode()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("error") != "" { // the user cancelled or the provider refused: nothing to redeem
		_, _ = h.svc.o.Store.ConsumePending(r.Context(), KindState, hashOf(q.Get("state")), h.svc.o.Now())
		back("sso_error", "provider")
		return
	}
	code, err := h.svc.Complete(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		back("sso_error", ErrorCode(err))
		return
	}
	back("sso_code", code)
}

func (h *Handler) actor(r *http.Request) (orgID, userID string) {
	p, _ := auth.PrincipalFrom(r.Context())
	return p.OrgID, p.UserID
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		var b errBody
		b.Error.Code, b.Error.Message, b.Error.Fields = "validation_failed", "invalid input", ve.Fields
		writeJSON(w, http.StatusUnprocessableEntity, b)
	case errors.Is(err, ErrNoEncryption):
		writeErr(w, http.StatusConflict, "no_encryption", err.Error())
	case errors.Is(err, ErrBadDomain), errors.Is(err, ErrFreeMail):
		writeErr(w, http.StatusUnprocessableEntity, map[bool]string{true: "free_mail", false: "bad_domain"}[errors.Is(err, ErrFreeMail)], err.Error())
	case errors.Is(err, ErrChallengeMissing):
		writeErr(w, http.StatusUnprocessableEntity, "challenge_missing", err.Error())
	case errors.Is(err, ErrDomainTaken):
		writeErr(w, http.StatusConflict, "domain_taken", err.Error())
	case errors.Is(err, ErrDomainInUse), errors.Is(err, ErrPolicyNeedsSSO):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	org, _ := h.actor(r)
	v, err := h.svc.GetConfig(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) putConfig(w http.ResponseWriter, r *http.Request) {
	var in ConfigInput
	if !decode(w, r, &in) {
		return
	}
	org, user := h.actor(r)
	v, err := h.svc.SaveConfig(r.Context(), org, user, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) deleteConfig(w http.ResponseWriter, r *http.Request) {
	org, user := h.actor(r)
	if err := h.svc.DeleteConfig(r.Context(), org, user); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) testConfig(w http.ResponseWriter, r *http.Request) {
	org, _ := h.actor(r)
	if err := h.svc.TestConfig(r.Context(), org); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	org, _ := h.actor(r)
	ds, err := h.svc.ListDomains(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": ds})
}

func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain string `json:"domain"`
	}
	if !decode(w, r, &in) {
		return
	}
	org, user := h.actor(r)
	d, err := h.svc.AddDomain(r.Context(), org, user, in.Domain)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *Handler) verifyDomain(w http.ResponseWriter, r *http.Request) {
	org, user := h.actor(r)
	d, err := h.svc.VerifyDomain(r.Context(), org, user, chi.URLParam(r, "domain"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) removeDomain(w http.ResponseWriter, r *http.Request) {
	org, user := h.actor(r)
	if err := h.svc.RemoveDomain(r.Context(), org, user, chi.URLParam(r, "domain")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type policyBody struct {
	RequireMFA bool `json:"require_mfa"`
	EnforceSSO bool `json:"enforce_sso"`
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request) {
	org, _ := h.actor(r)
	p, err := h.svc.GetPolicy(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policyBody{RequireMFA: p.RequireMFA, EnforceSSO: p.EnforceSSO})
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyBody
	if !decode(w, r, &in) {
		return
	}
	org, user := h.actor(r)
	p, err := h.svc.SetPolicy(r.Context(), org, user, in.RequireMFA, in.EnforceSSO)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policyBody{RequireMFA: p.RequireMFA, EnforceSSO: p.EnforceSSO})
}
