package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
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
	TrialLimit     Limit // per IP, trial accounts, default 3 / day
	UserLimit      Limit // per authenticated user on member endpoints, default 120 / min
	FailOpen       bool  // allow requests when the limiter backend is down

	// Refresh-token cookie. The refresh token never appears in a JSON body: it
	// travels in an HttpOnly, SameSite=Strict cookie scoped to the auth routes.
	CookieName   string // default "wfai_refresh"
	CookieSecure bool   // Secure attribute; turn off only for local plain-http development
	// AllowedOrigins is the browser origin allow-list (same list as CORS) used to
	// reject cross-site refresh/logout requests. The request's own host is always allowed.
	AllowedOrigins []string
	// SSO (optional) redeems the single-use code of a finished SSO sign-in: POST /auth/sso/exchange.
	SSO SSOExchanger
}

const (
	// DefaultCookieName is the refresh-token cookie.
	DefaultCookieName = "wfai_refresh"
	// CSRFHeader must be present on the cookie-authenticated endpoints. A custom
	// header cannot be sent by a cross-site form or simple request, and a
	// cross-origin fetch carrying it needs a CORS preflight that the allow-list decides.
	CSRFHeader = "X-WFAI-CSRF"
	cookiePath = "/api/v1/auth"
)

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
	def(&cfg.TrialLimit, 3, 24*time.Hour)
	def(&cfg.UserLimit, 120, time.Minute)
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultCookieName
	}
	return &Handler{svc: svc, cfg: cfg}
}

// setRefreshCookie stores the refresh token in the HttpOnly cookie.
func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cfg.CookieName, Value: token, Path: cookiePath,
		MaxAge:   int(h.svc.RefreshTTL() / time.Second),
		HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cfg.CookieName, Value: "", Path: cookiePath, MaxAge: -1,
		HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) refreshCookie(r *http.Request) string {
	c, err := r.Cookie(h.cfg.CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// writeSession sends the session with the refresh token moved into the cookie.
func (h *Handler) writeSession(w http.ResponseWriter, status int, s *Session) {
	h.setRefreshCookie(w, s.RefreshToken)
	view := *s
	view.RefreshToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, view)
}

// csrfOK guards the endpoints that act on the ambient cookie: the custom header
// must be present and a browser Origin, when sent, must be allowed or same-host.
func (h *Handler) csrfOK(r *http.Request) bool {
	if r.Header.Get(CSRFHeader) == "" {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser fetch: nothing ambient to abuse
	}
	for _, o := range h.cfg.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// Routes returns a router to mount, e.g. r.Mount("/api/v1/auth", h.Routes()):
//
//	POST   /register        public
//	POST   /login           public
//	POST   /refresh         public (refresh token in the HttpOnly cookie + X-WFAI-CSRF header; rotates it)
//	POST   /logout          public (cookie + X-WFAI-CSRF header; idempotent; clears the cookie)
//	GET    /me              authenticated
//	POST   /logout-all      authenticated
//	POST   /switch-org      authenticated {org_id}: session in another org of the user
//	GET    /config          public {enabled}
//	POST   /invitations/accept public {token, name?, password}
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
	r.With(lim("trial", h.cfg.TrialLimit)).Post("/trial", h.trial)
	r.With(lim("mfa", h.cfg.LoginLimit)).Post("/mfa/verify", h.mfaVerify)
	r.With(lim("mfa", h.cfg.LoginLimit)).Post("/mfa/enroll/setup", h.mfaEnrollSetup)
	r.With(lim("mfa", h.cfg.LoginLimit)).Post("/mfa/enroll/enable", h.mfaEnrollEnable)
	if h.cfg.SSO != nil {
		r.With(lim("sso", h.cfg.LoginLimit)).Post("/sso/exchange", h.ssoExchange)
	}
	r.With(lim("pwd-forgot", h.cfg.RegisterLimit)).Post("/password/forgot", h.forgotPassword)
	r.With(lim("pwd-reset", h.cfg.RegisterLimit)).Post("/password/reset", h.resetPassword)
	r.With(lim("verify", h.cfg.RegisterLimit)).Post("/verify", h.verify)
	r.With(lim("verify-resend", h.cfg.RegisterLimit)).Post("/verify/resend", h.resendVerification)
	r.Get("/config", AuthConfigHandlerV(true, h.svc.cfg.RequireVerification, h.svc.cfg.TrialEnabled, h.svc.PasswordResetEnabled()))
	// Accepting is public (the invitee may not have an account yet); throttled like registration.
	r.With(lim("invite-accept", h.cfg.RegisterLimit)).Post("/invitations/accept", h.acceptInvitation)

	r.Group(func(r chi.Router) {
		r.Use(h.svc.Authenticator())
		if h.cfg.Limiter != nil {
			r.Use(RateLimit(h.cfg.Limiter, h.cfg.UserLimit, UserKey("auth", h.cfg.TrustedProxies), h.cfg.FailOpen))
		}
		r.Get("/me", h.me)
		r.Post("/logout-all", h.logoutAll)
		r.Post("/mfa/setup", h.mfaSetup)
		r.Post("/mfa/enable", h.mfaEnable)
		r.Post("/mfa/disable", h.mfaDisable)
		r.Post("/switch-org", h.switchOrg)
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
	var vr *VerificationRequiredError
	if errors.As(err, &vr) {
		// The account exists but cannot sign in until the e-mail is verified.
		writeJSON(w, http.StatusAccepted, map[string]any{"verification_required": true, "email": vr.Email, "email_sent": vr.EmailSent})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.writeSession(w, http.StatusCreated, s)
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
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) trial(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Locale string `json:"locale"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	acct, err := h.svc.CreateTrial(r.Context(), in.Locale)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // it carries a password
	writeJSON(w, http.StatusCreated, acct)
}

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email  string `json:"email"`
		Locale string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), in.Email, in.Locale); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent) // the same answer whether or not the address has an account
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		writeServiceError(w, err)
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.VerifyMFA(r.Context(), in.MFAToken, in.Code, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) mfaEnrollSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MFAToken string `json:"mfa_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	info, err := h.svc.MFAEnrollSetup(r.Context(), in.MFAToken)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) mfaEnrollEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	sess, codes, err := h.svc.MFAEnrollComplete(r.Context(), in.MFAToken, in.Code, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.setRefreshCookie(w, sess.RefreshToken)
	view := *sess
	view.RefreshToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"session": view, "recovery_codes": codes}) // codes are shown once
}

func (h *Handler) ssoExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := h.cfg.SSO.Exchange(r.Context(), in.Code)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s, err := h.svc.SignInFederated(r.Context(), id, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) mfaSetup(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	info, err := h.svc.MFASetup(r.Context(), p.UserID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) mfaEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	codes, err := h.svc.MFAEnable(r.Context(), p.UserID, in.Code)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes}) // shown once
}

func (h *Handler) mfaDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.MFADisable(r.Context(), p.UserID, in.Password, in.Code); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	s, err := h.svc.Verify(r.Context(), in.Token, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email  string `json:"email"`
		Locale string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.ResendVerification(r.Context(), in.Email, in.Locale); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent) // same answer whether or not the address exists
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(r) {
		writeError(w, http.StatusForbidden, "csrf", "missing or invalid request origin")
		return
	}
	s, err := h.svc.Refresh(r.Context(), h.refreshCookie(r), h.meta(r))
	if err != nil {
		// A rejected token (expired, revoked or reused) is useless: drop it.
		if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrTokenReuse) {
			h.clearRefreshCookie(w)
		}
		writeServiceError(w, err)
		return
	}
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(r) {
		writeError(w, http.StatusForbidden, "csrf", "missing or invalid request origin")
		return
	}
	if err := h.svc.Logout(r.Context(), h.refreshCookie(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	u, err := h.svc.Me(r.Context(), p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	orgs, err := h.svc.Orgs(r.Context(), p.UserID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": u, "org_id": p.OrgID, "role": p.Role, "permissions": p.Role.Permissions(), "orgs": orgs,
	})
}

func (h *Handler) switchOrg(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrgID string `json:"org_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	s, err := h.svc.SwitchOrg(r.Context(), p, in.OrgID, h.meta(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// The cookie is about to point at the new organization's session: end the old family.
	_ = h.svc.Logout(r.Context(), h.refreshCookie(r))
	h.writeSession(w, http.StatusOK, s)
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := h.svc.LogoutAll(r.Context(), p.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	h.clearRefreshCookie(w)
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
	return AuthConfigHandlerV(enabled, false, false, false)
}

// AuthConfigHandlerV also tells the UI whether new accounts must verify their e-mail.
func AuthConfigHandlerV(enabled, verification, trial, passwordReset bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "registration": enabled, "verification": verification, "trial": trial, "password_reset": passwordReset, "mfa": enabled})
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
	h.writeSession(w, http.StatusOK, s)
}

// writeServiceError maps domain errors to HTTP responses without leaking
// internals. Unknown errors become an opaque 500.
func writeServiceError(w http.ResponseWriter, err error) {
	var ve *ValidationError
	var le *LockedError
	var re *RateLimitedError
	var mr *MFARequiredError
	var me *MFAEnrollmentRequiredError
	switch {
	case errors.As(err, &mr):
		// Not a failure: the password was right and the client must now present the second factor.
		writeJSON(w, http.StatusOK, map[string]any{"mfa_required": true, "mfa_token": mr.Token})
	case errors.As(err, &me):
		// Not a session: the organization requires a second factor and the user has none yet.
		if me.Token == "" {
			writeError(w, http.StatusForbidden, "mfa_enrollment_required", "this organization requires two-factor authentication: set it up first")
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"mfa_enrollment_required": true, "mfa_token": me.Token})
		}
	case errors.Is(err, ErrSSORequired):
		writeError(w, http.StatusForbidden, "sso_required", "this organization requires single sign-on")
	case errors.Is(err, ErrMFARequiredByOrg):
		writeError(w, http.StatusForbidden, "mfa_required_by_org", "an organization you belong to requires two-factor authentication")
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
	case errors.Is(err, ErrMFAInvalidCode):
		writeError(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid or already used code")
	case errors.Is(err, ErrMFAAlreadyEnabled):
		writeError(w, http.StatusConflict, "mfa_already_enabled", "two-factor authentication is already on")
	case errors.Is(err, ErrMFANotPending):
		writeError(w, http.StatusConflict, "mfa_not_pending", "start the setup first")
	case errors.Is(err, ErrTrialExpired):
		writeError(w, http.StatusForbidden, "trial_expired", "this trial account has expired")
	case errors.Is(err, ErrTrialDisabled):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrEmailNotVerified):
		writeError(w, http.StatusForbidden, "email_not_verified", "verify your email before signing in")
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
