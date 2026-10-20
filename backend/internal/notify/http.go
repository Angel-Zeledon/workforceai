package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Landing is the public page behind the links of the notifications:
//
//	GET  /api/v1/notify/a?t=<token>   shows the approval and a confirm button; changes nothing
//	POST /api/v1/notify/a             (form field t) consumes the token and decides
//
// GET never decides: mail scanners, chat link previews and browsers prefetch
// links. The POST is the confirmation. The token is the credential, so the
// page is served with no-store and no-referrer.
func (s *Service) Landing() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		loc := PickLocale(r.Header.Get("Accept-Language"))
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			tok := r.URL.Query().Get("t")
			c, ap, err := s.Peek(r.Context(), tok)
			if err != nil {
				s.page(w, loc, statusOf(err), "", failKey(err), s.openLink(c, ap, err), "", "")
				return
			}
			s.page(w, loc, http.StatusOK, tr(loc, "cLandT"), "", "", tok, summary(loc, ap, c.Decision))
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := r.ParseForm(); err != nil {
				s.page(w, loc, http.StatusBadRequest, "", "cBad", "", "", "")
				return
			}
			c, ap, err := s.Act(r.Context(), r.PostForm.Get("t"))
			if err != nil {
				s.page(w, loc, statusOf(err), "", failKey(err), s.openLink(c, ap, err), "", "")
				return
			}
			word := tr(loc, "cLandAp")
			if c.Decision == DecisionReject {
				word = tr(loc, "cLandRj")
			}
			s.page(w, loc, http.StatusOK, fmt.Sprintf(tr(loc, "cLandOK"), word), "", "", "", "")
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, ErrExpired), errors.Is(err, ErrUsed), errors.Is(err, ErrNotPending), errors.Is(err, domain.ErrConflict):
		return http.StatusGone
	case errors.Is(err, ErrNeedsApp), errors.Is(err, ErrLinksOff), errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrMalformed), errors.Is(err, ErrBadSignature), errors.Is(err, domain.ErrNotFound):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnavailable):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func failKey(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "cExp"
	case errors.Is(err, ErrUsed):
		return "cUsed"
	case errors.Is(err, ErrNotPending), errors.Is(err, domain.ErrConflict):
		return "cDone"
	case errors.Is(err, ErrNeedsApp), errors.Is(err, domain.ErrForbidden):
		return "cSess"
	case errors.Is(err, ErrLinksOff):
		return "cOff"
	case errors.Is(err, ErrMalformed), errors.Is(err, ErrBadSignature), errors.Is(err, domain.ErrNotFound), errors.Is(err, ErrUnavailable):
		return "cBad"
	}
	return "cFail"
}

// openLink offers the app when the person can finish there (a known approval
// that the link cannot decide); never for a forged or unknown link.
func (s *Service) openLink(c Claims, ap domain.Approval, err error) string {
	if ap.ID == "" || (!errors.Is(err, ErrNeedsApp) && !errors.Is(err, ErrExpired) && !errors.Is(err, ErrLinksOff)) {
		return ""
	}
	if s.appURL() == "" {
		return ""
	}
	return s.appURL() + "/approvals/" + ap.ID
}

func summary(loc string, ap domain.Approval, decision string) string {
	e := html.EscapeString
	act := tr(loc, "approve")
	if decision == DecisionReject {
		act = tr(loc, "reject")
	}
	return `<p class="t">` + e(Clip(oneLine(ap.Title), 200)) + `</p><p class="m">` + e(tr(loc, "risk")) + `: <b>` + e(ap.Risk) + `</b></p>` +
		`<p class="d">` + e(Clip(ap.Details, 500)) + `</p><p class="m">` + e(act) + `</p>`
}

// page renders the landing page. confirmTok != "" adds the confirm form.
func (s *Service) page(w http.ResponseWriter, loc string, status int, title, failKey, open, confirmTok, body string) {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + e(loc) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex">`)
	b.WriteString(`<title>AI Workforce OS</title><style>body{font-family:system-ui,sans-serif;background:#f5f5f4;color:#111;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}` +
		`main{background:#fff;border:1px solid #e5e5e5;border-radius:12px;padding:24px;max-width:460px;width:calc(100% - 32px)}h1{font-size:18px;margin:0 0 12px}` +
		`.t{font-weight:600}.m{color:#555;font-size:14px}.d{color:#444;font-size:14px;white-space:pre-wrap}` +
		`button,a.b{display:inline-block;margin-top:12px;padding:10px 18px;border:0;border-radius:6px;background:#047857;color:#fff;font-weight:600;font-size:15px;text-decoration:none;cursor:pointer}` +
		`.err{color:#b91c1c;font-weight:600}</style></head><body><main>`)
	if failKey != "" {
		b.WriteString(`<p class="err">` + e(tr(loc, failKey)) + `</p>`)
	} else {
		b.WriteString(`<h1>` + e(title) + `</h1>` + body)
	}
	if confirmTok != "" {
		b.WriteString(`<form method="post" action="/api/v1/notify/a"><input type="hidden" name="t" value="` + e(confirmTok) + `"><button type="submit">` + e(tr(loc, "confirm")) + `</button></form>`)
	}
	if open != "" {
		b.WriteString(`<p><a class="b" href="` + e(open) + `">` + e(tr(loc, "open")) + `</a></p>`)
	}
	b.WriteString(`</main></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

// ---- JSON API (behind authentication; the caller mounts it with permissions) ----

// API groups the authenticated handlers. Org and Actor come from the request
// context of the API layer, never from client input.
type API struct {
	Svc   *Service
	Org   func(*http.Request) string
	Actor func(*http.Request) string
	// Usage returns the numbers of the budget ledger for the plan card.
	Usage func(ctx context.Context, org string) (operatorCap, spent, reserved float64, err error)
	Fail  func(w http.ResponseWriter, err error)
	Write func(w http.ResponseWriter, status int, v any)
	// Audit (optional) records an administrative action of the request's actor.
	Audit func(r *http.Request, action, entity, entityID string, details map[string]any)
	// URLParam reads a path parameter (chi.URLParam in the API layer).
	URLParam func(r *http.Request, name string) string
}

// DeadLetters: GET /notify/outbox/dead - the messages that ran out of attempts.
func (a API) DeadLetters(w http.ResponseWriter, r *http.Request) {
	if a.Svc.Outbox == nil {
		a.Write(w, 200, map[string]any{"items": []DeadLetter{}, "enabled": false})
		return
	}
	items, err := a.Svc.ListDeadLetters(r.Context(), a.Org(r))
	if err != nil {
		a.Fail(w, err)
		return
	}
	a.Write(w, 200, map[string]any{"items": items, "enabled": true})
}

// RetryDeadLetter: POST /notify/outbox/dead/{id}/retry - back to pending with attempts reset; audited.
func (a API) RetryDeadLetter(w http.ResponseWriter, r *http.Request) {
	if a.Svc.Outbox == nil {
		a.Fail(w, errDeadLettersOff)
		return
	}
	id := a.URLParam(r, "id")
	dl, err := a.Svc.RetryDeadLetter(r.Context(), a.Org(r), id)
	if err != nil {
		a.Fail(w, err)
		return
	}
	if a.Audit != nil {
		a.Audit(r, "notify.outbox_retry", "notify_outbox", id, map[string]any{"channel": dl.Channel, "previous_attempts": dl.Attempts})
	}
	a.Write(w, 200, map[string]any{"id": id, "status": OutboxPending})
}

// GetSettings: GET /notify/settings.
func (a API) GetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := a.Svc.Settings.GetSettings(r.Context(), a.Org(r))
	if err != nil {
		a.Fail(w, err)
		return
	}
	a.Write(w, 200, st.View(a.Svc.EmailAvailable(), a.Svc.LinksAvailable()))
}

// PutSettings: PUT /notify/settings.
func (a API) PutSettings(w http.ResponseWriter, r *http.Request) {
	var in SettingsInput
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.Fail(w, errors.Join(domain.ErrInvalid, err))
		return
	}
	org := a.Org(r)
	cur, err := a.Svc.Settings.GetSettings(r.Context(), org)
	if err != nil {
		a.Fail(w, err)
		return
	}
	next, err := cur.Apply(in, a.Actor(r), a.Svc.now())
	if err != nil {
		a.Fail(w, err)
		return
	}
	next.OrgID = org
	if err := a.Svc.Settings.PutSettings(r.Context(), next); err != nil {
		a.Fail(w, err)
		return
	}
	a.Write(w, 200, next.View(a.Svc.EmailAvailable(), a.Svc.LinksAvailable()))
}

// Test: POST /notify/test.
func (a API) Test(w http.ResponseWriter, r *http.Request) {
	res, err := a.Svc.SendTest(r.Context(), a.Org(r))
	if err != nil {
		a.Fail(w, err)
		return
	}
	a.Write(w, 200, map[string]any{"sent": res})
}

// PlanUsage: GET /plan/usage — spent / estimated / cap for the UI.
func (a API) PlanUsage(w http.ResponseWriter, r *http.Request) {
	u, err := a.UsageOf(r.Context(), a.Org(r))
	if err != nil {
		a.Fail(w, err)
		return
	}
	a.Write(w, 200, u)
}

// UsageOf combines the plan with the ledger numbers.
func (a API) UsageOf(ctx context.Context, org string) (Usage, error) {
	capUSD, spent, reserved, err := a.Usage(ctx, org)
	if err != nil {
		return Usage{}, err
	}
	return a.Svc.Plans.Status(ctx, org, capUSD, spent, reserved), nil
}
