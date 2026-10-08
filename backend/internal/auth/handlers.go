package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

const maxBodyBytes = 1 << 20

// HandlerConfig configures the HTTP routes.
type HandlerConfig struct {
	// Limiter (optional but strongly recommended) enables per-IP throttling of
	// the unauthenticated endpoints and per-user throttling of the rest.
	Limiter        Limiter
	TrustedProxies []netip.Prefix
	LoginLimit     Limit // per IP, default 20 / 15m
	RegisterLimit  Limit // per IP, default 5 / hour
	RefreshLimit   Limit // per IP, default 60 / 15m
	UserLimit      Limit // per authenticated user on member endpoints, default 120 / min
	FailOpen       bool  // allow requests when the limiter backend is down
}

// Handler exposes the auth service over HTTP.
type Handler struct {
	svc *Service
	cfg HandlerConfig
}

func NewHandler(svc *Service, cfg HandlerConfig) *Handler {
	def := func(l *Limit, max int, w time.Duration) {
		if l.Max <= 0 || l.Window <= 0 {
			*l = Limit{Max: max, Window: w}
		}
	}
	def(&cfg.LoginLimit, 20, 15*time.Minute)
	def(&cfg.RegisterLimit, 5, time.Hour)
	def(&cfg.RefreshLimit, 60, 15*time.Minute)
	def(&cfg.UserLimit, 120, time.Minute)
	return &Handler{svc: svc, cfg: cfg}
}

// Routes returns a router to mount, e.g. r.Mount("/api/v1/auth", h.Routes()):
//
//	POST   /register        public
//	POST   /login           public
//	POST   /refresh         public (refresh token in body)
//	POST   /logout          public (refresh token in body; idempotent)
//	GET    /me              authenticated
//	POST   /logout-all      authenticated
//	GET    /members         members:read
//	POST   /members         members:invite   {email, role}
//	PATCH  /members/{id}    members:manage   {role}
//	DELETE /members/{id}    authenticated (self-leave) or members:manage
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	lim := func(prefix string, l Limit) func(http.Handler) http.Handler {
		if h.cfg.Limiter == nil {
			return func(next http.Handler) http.Handler { return next }
		}
		return RateLimit(h.cfg.Limiter, l, IPKey(prefix, h.cfg.TrustedProxies), h.cfg.FailOpen)
	}
	r.With(lim("register", h.cfg.RegisterLimit)).Post("/register", h.register)
	r.With(lim("login", h.cfg.LoginLimit)).Post("/login", h.login)
	r.With(lim("refresh", h.cfg.RefreshLimit)).Post("/refresh", h.refresh)
	r.With(lim("refresh", h.cfg.RefreshLimit)).Post("/logout", h.logout)
	r.Get("/config", AuthConfigHandler(true))
	// Accepting is public (the invitee may not have an account yet); throttled like registration.
	r.With(lim("invite-accept", h.cfg.RegisterLimit)).Post("/invitations/accept", h.acceptInvitation)

	r.Group(func(r chi.Router) {
		r.Use(h.svc.Authenticator())
		if h.cfg.Limiter != nil {
			r.Use(RateLimit(h.cfg.Limiter, h.cfg.UserLimit, UserKey("auth", h.cfg.TrustedProxies), h.cfg.FailOpen))
		}
		r.Get("/me", h.me)
		r.Post("/logout-all", h.logoutAll)
		r.With(RequirePermission(PermMembersRead)).Get("/members", h.listMembers)
		r.With(RequirePermission(PermMembersInvite)).Post("/members", h.addMember)
		r.With(RequirePermission(PermMembersManage)).Patch("/members/{id}", h.changeRole)
		r.Delete("/members/{id}", h.removeMember)
	})
	return r
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		}
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "unexpected trailing data")
		return false
	}
	return true
}

func (h *Handler) meta(r *http.Request) ClientMeta {
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	return ClientMeta{UserAgent: ua, IP: ClientIP(r, h.cfg.TrustedProxies)}
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in RegisterInput
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.Register(r.Context(), in, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in LoginInput
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.Login(r.Context(), in, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

type refreshBody struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var in refreshBody
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.Refresh(r.Context(), in.RefreshToken, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var in refreshBody
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.Logout(r.Context(), in.RefreshToken); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	u, err := h.svc.Me(r.Context(), p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": u, "org_id": p.OrgID, "role": p.Role, "permissions": p.Role.Permissions(),
	})
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.LogoutAll(r.Context(), p.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	ms, err := h.svc.ListMembers(r.Context(), p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if ms == nil {
		ms = []Member{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": ms})
}

type memberBody struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	var in memberBody
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	m, err := h.svc.AddMember(r.Context(), p, in.Email, in.Role)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *Handler) changeRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.ChangeMemberRole(r.Context(), p, chi.URLParam(r, "id"), in.Role); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.RemoveMember(r.Context(), p, chi.URLParam(r, "id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AuthConfigHandler serves the public GET /api/v1/auth/config, which tells the
// frontend whether to show the login (auth on) or the open demo (auth off).
func AuthConfigHandler(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "registration": enabled})
	}
}

// InvitationRoutes returns the router mounted at /api/v1/invitations:
//
//	GET    /       members:read
//	POST   /       members:invite  {email, role} -> invitation + one-time token
//	DELETE /{id}   members:invite  (revoke)
func (h *Handler) InvitationRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.svc.Authenticator())
	if h.cfg.Limiter != nil {
		r.Use(RateLimit(h.cfg.Limiter, h.cfg.UserLimit, UserKey("invitations", h.cfg.TrustedProxies), h.cfg.FailOpen))
	}
	r.With(RequirePermission(PermMembersRead)).Get("/", h.listInvitations)
	r.With(RequirePermission(PermMembersInvite)).Post("/", h.createInvitation)
	r.With(RequirePermission(PermMembersInvite)).Delete("/{id}", h.revokeInvitation)
	return r
}

func (h *Handler) createInvitation(w http.ResponseWriter, r *http.Request) {
	var in memberBody
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	inv, err := h.svc.CreateInvitation(r.Context(), p, in.Email, in.Role)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, inv)
}

func (h *Handler) listInvitations(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	invs, err := h.svc.ListInvitations(r.Context(), p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invs})
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.RevokeInvitation(r.Context(), p, chi.URLParam(r, "id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in AcceptInput
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.AcceptInvitation(r.Context(), in, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// writeServiceError maps domain errors to HTTP responses without leaking
// internals. Unknown errors become an opaque 500.
func writeServiceError(w http.ResponseWriter, err error) {
	var ve *ValidationError
	var le *LockedError
	var re *RateLimitedError
	switch {
	case errors.As(err, &ve):
		var b errorBody
		b.Error.Code, b.Error.Message, b.Error.Fields = "validation_failed", "invalid input", ve.Fields
		writeJSON(w, http.StatusUnprocessableEntity, b)
	case errors.As(err, &le):
		secs := int(time.Until(le.Until)/time.Second) + 1
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "account_locked", "too many failed attempts; try again later")
	case errors.As(err, &re):
		w.Header().Set("Retry-After", strconv.Itoa(int(re.RetryAfter/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
	case errors.Is(err, ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	case errors.Is(err, ErrTokenExpired), errors.Is(err, ErrInvalidToken), errors.Is(err, ErrTokenReuse), errors.Is(err, ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
	case errors.Is(err, ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "email already registered")
	case errors.Is(err, ErrAlreadyMember):
		writeError(w, http.StatusConflict, "already_member", "user is already a member")
	case errors.Is(err, ErrLastOwner):
		writeError(w, http.StatusConflict, "last_owner", "organization must keep at least one owner")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
