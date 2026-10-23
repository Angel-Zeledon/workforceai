package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/portal"
)

// Client portal (B13, docs/architecture/client-portal.md and 08-api.md).
//
//	GET    /portal/branding                         agents:read   name, accent color, has_logo
//	PUT    /portal/branding                         org:manage    {display_name, accent_color}
//	GET    /portal/branding/logo                    agents:read   the logo (image bytes)
//	PUT    /portal/branding/logo                    org:manage    raw PNG/JPEG/GIF body, re-encoded server side
//	DELETE /portal/branding/logo                    org:manage
//	GET    /projects/{id}/portal-links              org:manage    links of the project (never the token)
//	POST   /projects/{id}/portal-links              org:manage    {label, expires_in_days, deliverable_ids} -> token once
//	GET    /projects/{id}/portal-links/candidates   org:manage    deliverables the firm may share
//	PUT    /projects/{id}/portal-links/{lid}        org:manage    {label, deliverable_ids}
//	DELETE /projects/{id}/portal-links/{lid}        org:manage    revoke
//
// Public (no session; the token is the capability; rate limited per client address):
//
//	POST /public/portal/view   {token}  -> the allowlisted view (portal.View)
//	POST /public/portal/logo   {token}  -> the firm's logo
//
// The token travels in the BODY, never in a URL: it stays out of access logs, browser history and
// Referer headers (the page itself keeps it in the URL fragment, which browsers never send).

// Public limits: the token is a 256-bit secret, this only protects the database from floods.
var portalPublicLimit = auth.Limit{Max: 60, Window: time.Minute}

func (s *server) mountPortal(r chi.Router) {
	if s.Portal == nil {
		return
	}
	read, manage := s.can(auth.PermAgentsRead), s.can(auth.PermOrgManage)
	r.With(read).Get("/portal/branding", s.portalBranding)
	r.With(manage).Put("/portal/branding", s.portalSetBranding)
	r.With(read).Get("/portal/branding/logo", s.portalLogo)
	r.With(manage).Put("/portal/branding/logo", s.portalSetLogo)
	r.With(manage).Delete("/portal/branding/logo", s.portalDeleteLogo)
	r.With(manage).Get("/projects/{id}/portal-links", s.portalLinks)
	r.With(manage).Post("/projects/{id}/portal-links", s.portalCreateLink)
	r.With(manage).Get("/projects/{id}/portal-links/candidates", s.portalCandidates)
	r.With(manage).Put("/projects/{id}/portal-links/{lid}", s.portalUpdateLink)
	r.With(manage).Delete("/projects/{id}/portal-links/{lid}", s.portalRevokeLink)
}

func (s *server) mountPortalPublic(r chi.Router) {
	if s.Portal == nil {
		return
	}
	lim := s.PortalLimiter
	if lim == nil {
		lim = auth.NewMemoryLimiter()
	}
	r.Group(func(r chi.Router) {
		r.Use(publicPortalHeaders, auth.RateLimit(lim, portalPublicLimit, auth.IPKey("portal", s.TrustedProxies), true))
		r.Post("/public/portal/view", s.portalPublicView)
		r.Post("/public/portal/logo", s.portalPublicLogo)
	})
}

// publicPortalHeaders: nothing of a portal response may be cached, indexed, framed or leak a referrer.
func publicPortalHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
		next.ServeHTTP(w, r)
	})
}

func (s *server) portalFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, portal.ErrUnavailable):
		codeErr(w, http.StatusNotFound, "link_unavailable", "this link is not available")
	case errors.Is(err, portal.ErrNoLogo):
		codeErr(w, http.StatusNotFound, "no_logo", "no logo")
	default:
		s.fail(w, err)
	}
}

func writeImage(w http.ResponseWriter, mime string, data []byte, cache string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(data)
}

func (s *server) portalBranding(w http.ResponseWriter, r *http.Request) {
	b, err := s.Portal.Branding(r.Context())
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) portalSetBranding(w http.ResponseWriter, r *http.Request) {
	var in portal.BrandingInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.Portal.SetBranding(r.Context(), s.actor(r), in)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) portalLogo(w http.ResponseWriter, r *http.Request) {
	mime, data, err := s.Portal.Logo(r.Context())
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeImage(w, mime, data, "private, no-cache")
}

func (s *server) portalSetLogo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, portal.MaxLogoInput)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.fail(w, errTooLarge)
			return
		}
		s.fail(w, err)
		return
	}
	b, err := s.Portal.SetLogo(r.Context(), s.actor(r), data)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) portalDeleteLogo(w http.ResponseWriter, r *http.Request) {
	b, err := s.Portal.DeleteLogo(r.Context(), s.actor(r))
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *server) portalLinks(w http.ResponseWriter, r *http.Request) {
	v, err := s.Portal.ListLinks(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) portalCandidates(w http.ResponseWriter, r *http.Request) {
	v, err := s.Portal.Candidates(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) portalCreateLink(w http.ResponseWriter, r *http.Request) {
	var in portal.LinkInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Portal.CreateLink(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // the only copy of the token
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) portalUpdateLink(w http.ResponseWriter, r *http.Request) {
	var in portal.LinkInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Portal.UpdateLink(r.Context(), s.actor(r), chi.URLParam(r, "id"), chi.URLParam(r, "lid"), in)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) portalRevokeLink(w http.ResponseWriter, r *http.Request) {
	v, err := s.Portal.RevokeLink(r.Context(), s.actor(r), chi.URLParam(r, "id"), chi.URLParam(r, "lid"))
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// portalToken reads {"token": "..."} from a small body.
func (s *server) portalToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := s.decode(w, r, &body); err != nil {
		codeErr(w, http.StatusBadRequest, "invalid", "a JSON body with the token is required")
		return "", false
	}
	return body.Token, true
}

func (s *server) portalPublicView(w http.ResponseWriter, r *http.Request) {
	tok, ok := s.portalToken(w, r)
	if !ok {
		return
	}
	v, err := s.Portal.PublicView(r.Context(), tok)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) portalPublicLogo(w http.ResponseWriter, r *http.Request) {
	tok, ok := s.portalToken(w, r)
	if !ok {
		return
	}
	mime, data, err := s.Portal.PublicLogo(r.Context(), tok)
	if err != nil {
		s.portalFail(w, err)
		return
	}
	writeImage(w, mime, data, "no-store")
}
