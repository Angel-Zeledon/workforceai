package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/inboxes"
)

// Data inboxes (docs/architecture/data-inboxes.md and 08-api.md): a URL + secret token any software can push
// JSON, CSV or files to. Every push becomes a versioned, tainted file in the drive.
//
//	GET    /data-inboxes                    org:manage   every inbox of the organization (never the token)
//	POST   /data-inboxes                    org:manage   {name, folder, customer_id, allowed, max_bytes, rate_per_min} -> token ONCE
//	GET    /data-inboxes/{id}               org:manage
//	PUT    /data-inboxes/{id}               org:manage   {name, allowed, max_bytes, rate_per_min} (folder and customer are fixed)
//	POST   /data-inboxes/{id}/rotate        org:manage   new token ONCE; the old one stops working at once
//	DELETE /data-inboxes/{id}               org:manage   revoke for good (the files it wrote stay in the drive)
//	GET    /data-inboxes/{id}/pushes        org:manage   ?limit= (default 20, max 100): recent pushes, metadata only
//
// Public (no session; outside the user-session middleware):
//
//	POST /inbox/{id}   Authorization: Bearer <token>   body: application/json, text/csv, multipart/form-data or a file type
//
// The token is accepted ONLY in the Authorization header: a token in the query string is refused (URLs end up in
// access logs, proxies and shell history). Two rate limits apply: per client address before the token is checked
// (protects the database from floods and guessing) and per inbox (its rate_per_min) after. The body is capped
// before it is read: a declared Content-Length above the inbox limit is refused without reading, and the reader is
// wrapped in http.MaxBytesReader for chunked bodies.

// inboxPublicLimit is the per-address flood guard of the public endpoint (before authentication).
var inboxPublicLimit = auth.Limit{Max: 600, Window: time.Minute}

// inboxOut is an inbox as the admin API returns it: the view plus the absolute push URL when the public origin is
// known (PUBLIC_API_URL / UI_BASE_URL); "" otherwise, and the UI composes it from push_path and its own origin.
type inboxOut struct {
	inboxes.View
	PushURL string `json:"push_url"`
}

// inboxCreatedOut carries the only copy of the token (creation and rotation).
type inboxCreatedOut struct {
	inboxOut
	Token string `json:"token"`
}

func (s *server) inboxOut(v inboxes.View) inboxOut {
	out := inboxOut{View: v}
	if base := strings.TrimRight(s.InboxBaseURL, "/"); base != "" {
		out.PushURL = base + strings.TrimPrefix(v.PushPath, "/api/v1")
	}
	return out
}

func (s *server) mountInboxes(r chi.Router) {
	if s.Inboxes == nil {
		return
	}
	manage := s.can(auth.PermOrgManage)
	r.With(manage).Get("/data-inboxes", s.inboxList)
	r.With(manage).Post("/data-inboxes", s.inboxCreate)
	r.With(manage).Get("/data-inboxes/{id}", s.inboxGet)
	r.With(manage).Put("/data-inboxes/{id}", s.inboxUpdate)
	r.With(manage).Post("/data-inboxes/{id}/rotate", s.inboxRotate)
	r.With(manage).Delete("/data-inboxes/{id}", s.inboxRevoke)
	r.With(manage).Get("/data-inboxes/{id}/pushes", s.inboxPushes)
}

func (s *server) mountInboxesPublic(r chi.Router) {
	if s.Inboxes == nil {
		return
	}
	lim := s.InboxLimiter
	if lim == nil {
		lim = auth.NewMemoryLimiter()
	}
	r.Group(func(r chi.Router) {
		r.Use(publicInboxHeaders, auth.RateLimit(lim, inboxPublicLimit, auth.IPKey("inbox", s.TrustedProxies), true))
		r.Post("/inbox/{id}", func(w http.ResponseWriter, r *http.Request) { s.inboxPush(w, r, lim) })
	})
}

// publicInboxHeaders: an inbox answer is never cached nor sniffed.
func publicInboxHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// inboxFail maps the inbox errors to stable codes. public hides unexpected errors behind "internal".
func (s *server) inboxFail(w http.ResponseWriter, err error, public bool) {
	var mbe *http.MaxBytesError
	switch {
	case errors.Is(err, inboxes.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="data-inbox"`)
		codeErr(w, http.StatusUnauthorized, "unauthorized", "missing, unknown or revoked inbox token")
	case errors.Is(err, inboxes.ErrKindNotAllowed):
		codeErr(w, http.StatusUnsupportedMediaType, "kind_not_allowed", err.Error())
	case errors.Is(err, inboxes.ErrUnsupportedType), errors.Is(err, drive.ErrUnsupportedType):
		codeErr(w, http.StatusUnsupportedMediaType, "unsupported_media_type", err.Error())
	case errors.Is(err, inboxes.ErrTooLarge), errors.Is(err, drive.ErrTooLarge), errors.As(err, &mbe):
		codeErr(w, http.StatusRequestEntityTooLarge, "payload_too_large", "the payload exceeds the limit of this inbox")
	case errors.Is(err, drive.ErrQuota):
		codeErr(w, http.StatusInsufficientStorage, "quota_exceeded", "the storage quota of the organization would be exceeded")
	case errors.Is(err, inboxes.ErrBadPayload):
		codeErr(w, http.StatusBadRequest, "bad_payload", err.Error())
	case errors.Is(err, inboxes.ErrReadOnly), errors.Is(err, drive.ErrReadOnly):
		codeErr(w, http.StatusLocked, "read_only_mode", "the organization is in read-only mode")
	case errors.Is(err, inboxes.ErrKillSwitch):
		codeErr(w, http.StatusServiceUnavailable, "kill_switch_active", "the organization's kill switch is active")
	case errors.Is(err, inboxes.ErrInProgress):
		codeErr(w, http.StatusConflict, "push_in_progress", "a push with this idempotency key is in progress")
	case errors.Is(err, inboxes.ErrCustomerArchived):
		codeErr(w, http.StatusConflict, "customer_unavailable", "the customer of this inbox is archived or gone")
	case errors.Is(err, errTooLarge):
		codeErr(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
	case errors.Is(err, domain.ErrInvalid):
		codeErr(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		codeErr(w, http.StatusNotFound, "not_found", "inbox not found")
	case errors.Is(err, domain.ErrConflict):
		codeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrForbidden):
		codeErr(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		s.Log.Error("data inbox request failed", "err", err)
		if public {
			codeErr(w, http.StatusInternalServerError, "internal", "the push could not be stored")
			return
		}
		codeErr(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// ---- admin ----

func (s *server) inboxList(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Inboxes.List(r.Context())
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	out := make([]inboxOut, 0, len(vs))
	for _, v := range vs {
		out = append(out, s.inboxOut(v))
	}
	writeJSON(w, 200, out)
}

func (s *server) inboxGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.Inboxes.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	writeJSON(w, 200, s.inboxOut(v))
}

func (s *server) inboxCreate(w http.ResponseWriter, r *http.Request) {
	var in inboxes.Input
	if err := s.decode(w, r, &in); err != nil {
		s.inboxFail(w, err, false)
		return
	}
	c, err := s.Inboxes.Create(r.Context(), s.actor(r), in)
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // the only copy of the token
	writeJSON(w, http.StatusCreated, inboxCreatedOut{inboxOut: s.inboxOut(c.View), Token: c.Token})
}

func (s *server) inboxUpdate(w http.ResponseWriter, r *http.Request) {
	var in inboxes.Input
	if err := s.decode(w, r, &in); err != nil {
		s.inboxFail(w, err, false)
		return
	}
	v, err := s.Inboxes.Update(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	writeJSON(w, 200, s.inboxOut(v))
}

func (s *server) inboxRotate(w http.ResponseWriter, r *http.Request) {
	c, err := s.Inboxes.Rotate(r.Context(), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, inboxCreatedOut{inboxOut: s.inboxOut(c.View), Token: c.Token})
}

func (s *server) inboxRevoke(w http.ResponseWriter, r *http.Request) {
	v, err := s.Inboxes.Revoke(r.Context(), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	writeJSON(w, 200, s.inboxOut(v))
}

func (s *server) inboxPushes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ps, err := s.Inboxes.Pushes(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.inboxFail(w, err, false)
		return
	}
	writeJSON(w, 200, list(ps))
}

// ---- public push ----

// bearerToken reads "Authorization: Bearer <token>" (the scheme is case-insensitive).
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// cappedBody remembers whether http.MaxBytesReader cut the body, so a multipart body over the cap is reported as
// too large and not as malformed.
type cappedBody struct {
	io.ReadCloser
	hit bool
}

func (c *cappedBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		c.hit = true
	}
	return n, err
}

func (s *server) inboxPush(w http.ResponseWriter, r *http.Request, lim auth.Limiter) {
	q := r.URL.Query()
	if q.Has("token") || q.Has("access_token") {
		codeErr(w, http.StatusBadRequest, "token_in_query", "send the token in the Authorization header (Bearer), never in the URL; rotate this token")
		return
	}
	tok := bearerToken(r)
	if tok == "" {
		s.inboxFail(w, inboxes.ErrUnauthorized, true)
		return
	}
	org, ibx, err := s.Inboxes.Authenticate(r.Context(), chi.URLParam(r, "id"), tok)
	if err != nil {
		s.inboxFail(w, err, true)
		return
	}
	// Per-inbox limit (its rate_per_min). Fail closed: an unreadable limiter must not turn into an open door.
	l := auth.Limit{Max: max(ibx.RatePerMin, 1), Window: time.Minute}
	res, err := lim.Allow(r.Context(), "inbox:id:"+ibx.ID, l)
	if err != nil {
		s.Log.Warn("data inbox rate limiter unavailable", "err", err)
		codeErr(w, http.StatusServiceUnavailable, "unavailable", "rate limiter unavailable, retry later")
		return
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.Max))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
	if !res.Allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter/time.Second)+1))
		codeErr(w, http.StatusTooManyRequests, "rate_limited", "too many pushes to this inbox, retry later")
		return
	}
	// The body cap is enforced before anything is read.
	capBytes := s.Inboxes.MaxBody(ibx)
	if r.ContentLength > capBytes {
		s.inboxFail(w, inboxes.ErrTooLarge, true)
		return
	}
	body := &cappedBody{ReadCloser: http.MaxBytesReader(w, r.Body, capBytes)}
	out, err := s.Inboxes.Push(r.Context(), org, ibx, inboxes.PushInput{ContentType: r.Header.Get("Content-Type"),
		FileName: r.Header.Get("X-Filename"), IdemKey: r.Header.Get("X-Idempotency-Key"), Body: body})
	if err != nil {
		if body.hit {
			err = inboxes.ErrTooLarge
		}
		s.inboxFail(w, err, true)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}
