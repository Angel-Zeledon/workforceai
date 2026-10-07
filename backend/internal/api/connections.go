package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/vault"
)

// Connections API (additive, v1). See docs/architecture/08-api.md section 13
// and integrations-credentials.md section 12. Responses NEVER contain secrets:
// credential fields are write-only; only {kind, hint, version, expires_at}
// metadata is returned.

// codeErr writes {"error": message, "code": stable_code}.
func codeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

var leadingCode = regexp.MustCompile(`^([a-z][a-z_]+)(:| )`)

// connErr maps domain errors to stable codes. Raw provider text never gets here.
func (s *server) connErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, connections.ErrNotFound), errors.Is(err, domain.ErrNotFound):
		codeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, vault.ErrNoKEK):
		codeErr(w, http.StatusServiceUnavailable, "kek_missing",
			"credential storage is not configured (set CONNECTIONS_KEK); use mode=simulated")
	case errors.Is(err, connections.ErrOAuthUnavailable):
		codeErr(w, http.StatusServiceUnavailable, "oauth_not_configured", err.Error())
	case errors.Is(err, connections.ErrNeedsSecondApprover):
		codeErr(w, http.StatusConflict, "second_approver_required", err.Error())
	case errors.Is(err, controls.ErrNoReason):
		codeErr(w, http.StatusBadRequest, "reason_required", err.Error())
	case errors.Is(err, connections.ErrInvalid), errors.Is(err, controls.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		msg := err.Error()
		msg = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(msg, "connections: invalid input: "), "controls: invalid input: "), "invalid input: ")
		code := "invalid"
		if m := leadingCode.FindStringSubmatch(msg); m != nil {
			code = m[1]
		}
		codeErr(w, http.StatusBadRequest, code, msg)
	case errors.Is(err, domain.ErrForbidden):
		codeErr(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, connections.ErrConflict), errors.Is(err, domain.ErrConflict):
		codeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, errTooLarge):
		codeErr(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	default:
		s.Log.Error("connections request failed", "err", err)
		codeErr(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// has reports whether the caller holds perm (always true without auth: the
// demo organization behaves as owner).
func (s *server) has(r *http.Request, perm auth.Permission) bool {
	if !s.AuthEnabled {
		return true
	}
	p, ok := auth.PrincipalFrom(r.Context())
	return ok && p.Can(perm)
}

func (s *server) actor(r *http.Request) string { return application.ActorFrom(r.Context(), "user") }

func (s *server) mountOAuthCallback(r chi.Router) {
	if s.Conns == nil {
		return
	}
	r.Get("/connections/oauth/callback", s.oauthCallback)
}

func (s *server) mountConnections(r chi.Router) {
	if s.Conns == nil {
		return
	}
	read := s.can(auth.PermConnectionsRead)
	manage := s.can(auth.PermConnectionsManage)
	r.With(read).Get("/connection-providers", s.listProviders)
	r.With(read).Get("/connections", s.listConnections)
	r.With(manage).Post("/connections", s.createConnection)
	r.With(read).Get("/connections/{id}", s.getConnection)
	r.With(manage).Patch("/connections/{id}", s.patchConnection)
	r.With(manage).Post("/connections/{id}/oauth/start", s.oauthStart)
	r.With(manage).Post("/connections/{id}/scopes", s.addScopes)
	r.With(manage).Put("/connections/{id}/credential", s.putCredential)
	r.With(manage).Post("/connections/{id}/test", s.testConnection)
	r.With(s.can(auth.PermConnectionsRevoke)).Post("/connections/{id}/suspend", s.suspendConnection)
	r.With(s.can(auth.PermConnectionsRevoke)).Post("/connections/{id}/resume", s.resumeConnection)
	r.With(s.can(auth.PermConnectionsRevoke)).Post("/connections/{id}/revoke", s.revokeConnection)
	r.With(read).Get("/connections/{id}/grants", s.listGrants)
	r.With(s.can(auth.PermConnectionsGrant)).Put("/connections/{id}/grants/{agent_id}", s.putGrant)
	r.With(s.can(auth.PermConnectionsGrant)).Post("/connections/{id}/grants/{agent_id}/approve", s.approveGrant)
	// {approve:true} form of the same second-human approval
	r.With(s.can(auth.PermConnectionsGrant)).Post("/connections/{id}/grants/{agent_id}", s.approveGrantBody)
	r.With(s.can(auth.PermConnectionsRevoke)).Delete("/connections/{id}/grants/{agent_id}", s.deleteGrant)
	r.With(read).Get("/agents/{id}/connections", s.agentConnections)
	usage := s.can(auth.PermConnectionsUsageRead)
	r.With(usage).Get("/connections/{id}/usage", s.listUsage)
	r.With(usage).Get("/connections/{id}/usage/summary", s.usageSummary)
	r.With(read).Get("/connections/{id}/limits", s.getLimits)
	r.With(manage).Put("/connections/{id}/limits", s.putLimits)
	if s.Gateway != nil {
		r.With(read).Get("/connection-holds", s.listHolds)
		// cancel-hold: the author or whoever may decide approvals (members cancel their own sends).
		r.With(s.can(auth.PermRequestsCreate)).Post("/tool-calls/{id}/cancel-hold", s.cancelHold)
		r.With(s.can(auth.PermRequestsRead)).Get("/requests/{id}/plan", s.getPlan)
		s.mountOutbox(r)
		r.With(s.can(auth.PermRequestsCreate)).Patch("/requests/{id}/plan", s.patchPlan)
		r.With(s.can(auth.PermRequestsCreate)).Post("/requests/{id}/plan/approve", s.approvePlan)
		r.With(s.can(auth.PermRequestsCreate)).Post("/requests/{id}/plan/reject", s.rejectPlan)
	}
}

func (s *server) approvalContext(id string) map[string]any {
	if s.Gateway == nil {
		return nil
	}
	p, ok := s.Gateway.ApprovalContext(id)
	if !ok {
		return nil
	}
	return map[string]any{"account": p.Account, "recipients": p.Recipients, "reversibility": p.Reversibility,
		"tainted": p.Tainted, "external_origin": p.ExternalOrigin, "hold_seconds": p.HoldSeconds, "flags": p.Flags, "args_hash": p.ArgsHash, "outbox_id": p.OutboxID}
}

func (s *server) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Conns.Catalog())
}

func (s *server) listConnections(w http.ResponseWriter, r *http.Request) {
	v, err := s.Conns.List(r.Context(), s.org(r), r.URL.Query().Get("provider"), r.URL.Query().Get("status"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) createConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider      string                    `json:"provider"`
		Kind          string                    `json:"kind"`
		Label         string                    `json:"label"`
		Capabilities  []string                  `json:"capabilities"`
		ResourceScope connections.ResourceScope `json:"resource_scope"`
		Mode          string                    `json:"mode"`
		Secret        string                    `json:"secret"`
		ExpiresAt     *time.Time                `json:"expires_at"`
		// Bring your own OAuth app (self-hosted). The secret is write-only.
		OAuthClientID     string `json:"oauth_client_id"`
		OAuthClientSecret string `json:"oauth_client_secret"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	in := connections.CreateInput{Provider: body.Provider, Kind: body.Kind, Label: body.Label, Capabilities: body.Capabilities,
		ResourceScope: body.ResourceScope, Mode: body.Mode, ExpiresAt: body.ExpiresAt, Actor: s.actor(r)}
	if body.Secret != "" {
		sec := vault.SecretFromString(body.Secret)
		defer sec.Zero()
		in.Secret = &sec
	}
	in.OAuthClientID = body.OAuthClientID
	if body.OAuthClientSecret != "" {
		cs := vault.SecretFromString(body.OAuthClientSecret)
		defer cs.Zero()
		in.OAuthClientSecret = &cs
	}
	body.Secret, body.OAuthClientSecret = "", "" // write-only: drop our copy of the plain text
	c, err := s.Conns.Create(r.Context(), s.org(r), in)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) getConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.Conns.Get(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(c.UpdatedAt.UnixNano(), 36)+`"`)
	writeJSON(w, 200, c)
}

func (s *server) patchConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if im := r.Header.Get("If-Match"); im != "" {
		cur, err := s.Conns.Get(r.Context(), s.org(r), id)
		if err != nil {
			s.connErr(w, err)
			return
		}
		if im != `"`+strconv.FormatInt(cur.UpdatedAt.UnixNano(), 36)+`"` {
			codeErr(w, http.StatusPreconditionFailed, "precondition_failed", "the connection changed; reload it")
			return
		}
	}
	var body struct {
		Label         *string                    `json:"label"`
		Limits        *connections.Limits        `json:"limits"`
		ResourceScope *connections.ResourceScope `json:"resource_scope"`
		ReadOnly      *bool                      `json:"read_only"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	c, err := s.Conns.Patch(r.Context(), s.org(r), id, connections.PatchInput{Label: body.Label, Limits: body.Limits,
		ResourceScope: body.ResourceScope, ReadOnly: body.ReadOnly, Actor: s.actor(r)})
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) oauthStart(w http.ResponseWriter, r *http.Request) {
	u, exp, err := s.Conns.OAuthStart(r.Context(), s.org(r), chi.URLParam(r, "id"), s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"auth_url": u, "expires_at": exp})
}

// oauthCallback is public: it is verified by the single-use state. It always
// answers with a redirect to the UI and never forwards provider text.
func (s *server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	next, err := s.Conns.OAuthCallback(r.Context(), q.Get("code"), q.Get("state"), q.Get("error"))
	if err != nil {
		code := "oauth_failed"
		if errors.Is(err, connections.ErrInvalid) {
			code = "invalid_state"
		}
		http.Redirect(w, r, "/connections?error="+code, http.StatusFound)
		return
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (s *server) addScopes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Add []string `json:"add"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	c, err := s.Conns.Get(r.Context(), s.org(r), id)
	if err != nil {
		s.connErr(w, err)
		return
	}
	m, _ := s.Conns.Manifest(c.Provider)
	for _, a := range body.Add {
		if m.Capabilities[a].SideEffects && !s.has(r, auth.PermConnectionsGrantWrite) {
			codeErr(w, http.StatusForbidden, "forbidden", "adding write capabilities needs connections:grant:write")
			return
		}
	}
	if _, err := s.Conns.AddCapabilities(r.Context(), s.org(r), id, body.Add, s.actor(r)); err != nil {
		s.connErr(w, err)
		return
	}
	resp := map[string]any{}
	if c.Mode == connections.ModeLive {
		u, exp, err := s.Conns.OAuthStart(r.Context(), s.org(r), id, s.actor(r))
		if err != nil {
			s.connErr(w, err)
			return
		}
		resp["auth_url"], resp["expires_at"] = u, exp
	}
	writeJSON(w, 200, resp)
}

func (s *server) putCredential(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"secret"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	sec := vault.SecretFromString(body.Secret)
	defer sec.Zero()
	body.Secret = ""
	c, err := s.Conns.RotateCredential(r.Context(), s.org(r), chi.URLParam(r, "id"), sec, s.actor(r), r.URL.Query().Get("destroy_previous") == "true")
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) testConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res, err := s.Conns.Test(r.Context(), s.org(r), id, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	c, _ := s.Conns.Get(r.Context(), s.org(r), id)
	writeJSON(w, 200, map[string]any{"status": c.Status, "ok": res.OK, "code": res.Code, "latency_ms": res.LatencyMS,
		"account_label": c.AccountLabel, "granted_capabilities": c.GrantedCapabilities})
}

func (s *server) suspendConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.Conns.Suspend(r.Context(), s.org(r), chi.URLParam(r, "id"), s.actor(r), "manual")
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

// resumeConnection: after a kill switch only the owner (controls:release) may resume.
func (s *server) resumeConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cur, err := s.Conns.Get(r.Context(), s.org(r), id)
	if err != nil {
		s.connErr(w, err)
		return
	}
	if cur.StatusReason == "kill_switch" && !s.has(r, auth.PermControlsRelease) {
		codeErr(w, http.StatusForbidden, "forbidden", "only the owner can resume a connection suspended by the kill switch")
		return
	}
	c, err := s.Conns.Resume(r.Context(), s.org(r), id, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) revokeConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	c, err := s.Conns.Revoke(r.Context(), s.org(r), chi.URLParam(r, "id"), body.ConfirmName, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) listGrants(w http.ResponseWriter, r *http.Request) {
	g, err := s.Conns.Grants(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, list(g))
}

func (s *server) putGrant(w http.ResponseWriter, r *http.Request) {
	id, agent := chi.URLParam(r, "id"), chi.URLParam(r, "agent_id")
	var body struct {
		Capabilities     []string                  `json:"capabilities"`
		ResourceScope    connections.ResourceScope `json:"resource_scope"`
		Constraints      connections.Constraints   `json:"constraints"`
		MaxRisk          string                    `json:"max_risk"`
		AutonomyOverride string                    `json:"autonomy_override"`
		Limits           connections.Limits        `json:"limits"`
		RedactionProfile string                    `json:"redaction_profile"`
		ValidUntil       *time.Time                `json:"valid_until"`
		Alias            string                    `json:"alias"`
		IsDefault        *bool                     `json:"is_default"`
		ConfirmName      string                    `json:"confirm_name"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	ctx, org := r.Context(), s.org(r)
	conn, err := s.Conns.Get(ctx, org, id)
	if err != nil {
		s.connErr(w, err)
		return
	}
	if _, err := s.Store.GetAgent(ctx, org, agent); err != nil {
		s.connErr(w, err)
		return
	}
	m, _ := s.Conns.Manifest(conn.Provider)
	writes := false
	for _, c := range body.Capabilities {
		writes = writes || m.Capabilities[c].SideEffects
	}
	if writes {
		// Extending an agent with side effects: owner/admin permission plus an
		// explicit confirmation (typing the connection name).
		if !s.has(r, auth.PermConnectionsGrantWrite) {
			codeErr(w, http.StatusForbidden, "forbidden", "granting write capabilities needs connections:grant:write")
			return
		}
		if strings.TrimSpace(body.ConfirmName) != conn.Label {
			codeErr(w, http.StatusBadRequest, "confirmation_required", "confirm_name must equal the connection label to grant write capabilities")
			return
		}
	}
	g, err := s.Conns.PutGrant(ctx, org, id, agent, connections.GrantInput{Capabilities: body.Capabilities, ResourceScope: body.ResourceScope,
		Constraints: body.Constraints, MaxRisk: body.MaxRisk, AutonomyOverride: body.AutonomyOverride, Limits: body.Limits,
		RedactionProfile: body.RedactionProfile, ValidUntil: body.ValidUntil, Alias: body.Alias, IsDefault: body.IsDefault,
		AllowNoRedaction: s.has(r, auth.PermControlsRelease)}, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	status := 200
	if g.Status == connections.GrantPendingApproval {
		status = http.StatusAccepted // waiting for a second human
	}
	writeJSON(w, status, g)
}

func (s *server) approveGrant(w http.ResponseWriter, r *http.Request) {
	if !s.has(r, auth.PermConnectionsGrantWrite) {
		codeErr(w, http.StatusForbidden, "forbidden", "approving write grants needs connections:grant:write")
		return
	}
	g, err := s.Conns.ApproveGrant(r.Context(), s.org(r), chi.URLParam(r, "id"), chi.URLParam(r, "agent_id"), s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, g)
}

func (s *server) approveGrantBody(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	if !body.Approve {
		codeErr(w, http.StatusBadRequest, "invalid", "send {\"approve\": true} to approve, or PUT to change the grant")
		return
	}
	s.approveGrant(w, r)
}

func (s *server) deleteGrant(w http.ResponseWriter, r *http.Request) {
	if err := s.Conns.RevokeGrant(r.Context(), s.org(r), chi.URLParam(r, "id"), chi.URLParam(r, "agent_id"), s.actor(r)); err != nil {
		s.connErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) agentConnections(w http.ResponseWriter, r *http.Request) {
	v, err := s.Conns.AgentConnections(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) listUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := connections.UsageFilter{ConnectionID: chi.URLParam(r, "id"), AgentID: q.Get("agent_id"), Result: q.Get("result"), Before: q.Get("cursor")}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = n
	}
	for k, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(k); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				codeErr(w, http.StatusBadRequest, "invalid", k+" must be RFC3339")
				return
			}
			*dst = &t
		}
	}
	if f.Result != "" && f.Result != "allowed" && f.Result != "needs_approval" && f.Result != "denied" {
		codeErr(w, http.StatusBadRequest, "invalid", "result must be allowed, needs_approval or denied")
		return
	}
	rows, err := s.Conns.Usage(r.Context(), s.org(r), f)
	if err != nil {
		s.connErr(w, err)
		return
	}
	next := ""
	if lim := f.Limit; (lim == 0 && len(rows) == 100) || (lim > 0 && len(rows) == lim) {
		next = rows[len(rows)-1].ID
	}
	writeJSON(w, 200, map[string]any{"items": rows, "next_cursor": next})
}

func (s *server) usageSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Conns.UsageSummary(r.Context(), s.org(r), chi.URLParam(r, "id"), r.URL.Query().Get("period"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, sum)
}

func (s *server) getLimits(w http.ResponseWriter, r *http.Request) {
	c, err := s.Conns.Get(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c.Limits)
}

func (s *server) putLimits(w http.ResponseWriter, r *http.Request) {
	var l connections.Limits
	if err := s.decode(w, r, &l); err != nil {
		s.connErr(w, err)
		return
	}
	c, err := s.Conns.PutLimits(r.Context(), s.org(r), chi.URLParam(r, "id"), l, s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, c.Limits)
}

// ---- holds (deferred sends) and plan review ----

func (s *server) listHolds(w http.ResponseWriter, r *http.Request) {
	h, err := s.Gateway.Holds(r.Context(), s.org(r), r.URL.Query().Get("status"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, h)
}

func (s *server) cancelHold(w http.ResponseWriter, r *http.Request) {
	h, err := s.Gateway.CancelHold(r.Context(), s.org(r), chi.URLParam(r, "id"), s.actor(r))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, s.Gateway.HoldItem(r.Context(), h))
}

func (s *server) getPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.Gateway.PlanGet(s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, s.planShape(r, v))
}

func (s *server) patchPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RemoveTaskIDs     []string `json:"remove_task_ids"`
		NoExternalActions *bool    `json:"no_external_actions"`
		Note              *string  `json:"note"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.connErr(w, err)
		return
	}
	v, err := s.Gateway.PlanPatch(s.org(r), chi.URLParam(r, "id"), body.RemoveTaskIDs, body.NoExternalActions, body.Note)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, s.planShape(r, v))
}

func (s *server) approvePlan(w http.ResponseWriter, r *http.Request) { s.decidePlan(w, r, true) }
func (s *server) rejectPlan(w http.ResponseWriter, r *http.Request)  { s.decidePlan(w, r, false) }

func (s *server) decidePlan(w http.ResponseWriter, r *http.Request, approve bool) {
	v, err := s.Gateway.PlanDecide(r.Context(), s.org(r), chi.URLParam(r, "id"), s.actor(r), approve)
	if err != nil {
		s.connErr(w, err)
		return
	}
	writeJSON(w, 200, s.planShape(r, v))
}
