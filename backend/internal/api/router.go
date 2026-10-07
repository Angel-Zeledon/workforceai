// Package api exposes the REST API and the WebSocket endpoint.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/projects"
)

// Deps are the collaborators of the HTTP layer.
type Deps struct {
	Cfg       application.Config
	Queries   *application.Queries
	Orch      *application.Orchestrator
	Approvals *application.Approvals
	// Audit serves GET /audit, /audit/export and /audit/verify; Policy-related
	// rules are served from OrgConfig (/policy/rules). Both optional.
	Audit   *application.AuditService
	Store   application.Store
	Runtime application.Runtime
	Hub     *events.Hub
	Log     *slog.Logger

	// AuthEnabled protects /api/v1 (except /healthz and /auth/*) and /ws with
	// JWT bearer tokens; the tenant then comes from the token. Auth must be
	// set and AuthRoutes (login/register/refresh...) is mounted at /api/v1/auth.
	AuthEnabled bool
	Auth        *auth.Service
	// OrgConfig serves workflow templates, onboarding, org settings/tone and
	// schedules (see orgconfig.go). Optional: nil leaves those routes unmounted.
	OrgConfig  *application.OrgConfig
	AuthRoutes http.Handler
	// Conns, Controls and Gateway serve the connections / controls endpoints
	// (see connections.go and controls.go). All optional: when nil the routes
	// are not mounted and the API is exactly as before.
	Conns    *connections.Service
	Controls *controls.Service
	Gateway  *gateway.Gateway
	// Projects serves /projects, /project-templates and /approvals/batch
	// (projects.go); Artifacts serves /artifacts and /projects/{id}/workspace
	// (artifacts.go). Both optional: when nil the routes are not mounted.
	Projects  *projects.Service
	Artifacts *artifacts.Service
	// AllowedOrigins is the CORS/WebSocket origin allow-list (see originPolicy).
	AllowedOrigins []string
	// EnableDemoReset registers POST /api/v1/demo/reset (admin role when auth is on).
	EnableDemoReset bool
	// MaxBodyBytes caps JSON request bodies (default 1 MiB).
	MaxBodyBytes int64
	// WSMaxAge closes a WebSocket after this long so that clients must
	// reconnect with a fresh access token (0 = unlimited; main sets the access
	// token lifetime when auth is on).
	WSMaxAge time.Duration
}

type server struct {
	Deps
	origins  originPolicy
	upgrader websocket.Upgrader
	seeded   sync.Map // org id -> struct{}: orgs whose agents were verified
}

// NewRouter builds the chi router.
func NewRouter(d Deps) http.Handler {
	if d.MaxBodyBytes <= 0 {
		d.MaxBodyBytes = 1 << 20
	}
	s := &server{Deps: d, origins: newOriginPolicy(d.AllowedOrigins, d.AuthEnabled)}
	s.upgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 4096, CheckOrigin: s.checkOrigin}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, s.cors)
	r.Get("/healthz", s.healthz)
	r.With(s.wsToken, s.authenticate, s.tenant).Get("/ws", s.ws)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/healthz", s.healthz)
		if d.AuthEnabled && d.AuthRoutes != nil {
			r.Mount("/auth", d.AuthRoutes)
		}
		// The OAuth callback is public (the browser comes back from the provider
		// without a bearer token); it is verified by its single-use state.
		s.mountOAuthCallback(r)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate, s.tenant)
			r.With(s.can(auth.PermAgentsRead)).Get("/agents", s.listAgents)
			r.With(s.can(auth.PermAgentsRead)).Get("/agents/{id}", s.getAgent)
			r.With(s.can(auth.PermAgentsRead)).Get("/agents/{id}/detail", s.agentDetail)
			r.With(s.can(auth.PermTasksRead)).Get("/tasks", s.listTasks)
			r.With(s.can(auth.PermTasksRead)).Get("/tasks/{id}", s.getTask)
			r.With(s.can(auth.PermRequestsCreate)).Post("/requests", s.createRequest)
			r.With(s.can(auth.PermRequestsRead)).Get("/requests", s.listRequests)
			r.With(s.can(auth.PermRequestsRead)).Get("/requests/{id}", s.getRequest)
			r.With(s.can(auth.PermConversationsRead)).Get("/conversations", s.listConversations)
			r.With(s.can(auth.PermConversationsRead)).Get("/conversations/{id}/messages", s.listMessages)
			r.With(s.can(auth.PermConversationsPost)).Post("/messages", s.postChat)
			r.With(s.can(auth.PermConversationsPost)).Post("/conversations/{id}/messages", s.postMessage)
			r.With(s.can(auth.PermApprovalsRead)).Get("/approvals", s.listApprovals)
			// Deciding approvals is a privileged action: admin/owner only.
			r.With(s.can(auth.PermApprovalsDecide)).Post("/approvals/{id}/decision", s.decide)
			r.With(s.can(auth.PermReportsRead)).Get("/reports", s.listReports)
			r.With(s.can(auth.PermReportsRead)).Get("/reports/{id}", s.getReport)
			r.With(s.can(auth.PermActivityRead)).Get("/activity", s.activity)
			r.With(s.can(auth.PermMetricsRead)).Get("/metrics", s.metrics)
			s.mountOrgConfig(r)
			s.mountAudit(r)
			s.mountCost(r)
			s.mountConnections(r)
			s.mountControls(r)
			s.mountProjects(r)
			s.mountArtifacts(r)
			if d.EnableDemoReset {
				r.With(s.requireRole(auth.RoleAdmin)).Post("/demo/reset", s.reset)
			}
		})
	})
	return r
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, errTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	if status == http.StatusInternalServerError {
		s.Log.Error("request failed", "err", err)
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// errTooLarge marks a body that exceeded MaxBodyBytes (HTTP 413).
var errTooLarge = errors.New("request body too large")

// decode reads a JSON body capped at MaxBodyBytes. The real ResponseWriter is
// handed to http.MaxBytesReader so the server closes the connection on oversized bodies.
func (s *server) decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge
		}
		return errors.Join(domain.ErrInvalid, err)
	}
	return nil
}

// list guarantees a JSON array instead of null.
func list[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// org is the tenant of the request: from the JWT when auth is on (set by the
// tenant middleware), the fixed demo org otherwise. Never from client input.
func (s *server) org(r *http.Request) string {
	return application.OrgFrom(r.Context(), s.Cfg.OrgID)
}

// ---- handlers ----

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]string{"status": "ok", "runtime": "up"}
	mode, err := s.Runtime.Health(ctx)
	if err != nil {
		out["runtime"] = "down"
	} else {
		out["runtime_mode"] = mode
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) listAgents(w http.ResponseWriter, r *http.Request) {
	a, err := s.Queries.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(a))
}

func (s *server) getAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.Queries.Agent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *server) agentDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.Queries.AgentDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Tasks, d.Conversations, d.Memory, d.RecentActivity = list(d.Tasks), list(d.Conversations), list(d.Memory), list(d.RecentActivity)
	writeJSON(w, 200, d)
}

func (s *server) listTasks(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.ListTasks(r.Context(), s.org(r), r.URL.Query().Get("agent_id"), r.URL.Query().Get("status"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(t))
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTask(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *server) createRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
		// BudgetCapUSD is the optional hard cap of this request (0 = configured default).
		BudgetCapUSD float64 `json:"budget_cap_usd"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.Orch.Submit(application.WithBudgetCap(r.Context(), body.BudgetCapUSD), body.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": id})
}

func (s *server) listRequests(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ListRequests(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) getRequest(w http.ResponseWriter, r *http.Request) {
	d, err := s.Queries.RequestDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Tasks = list(d.Tasks)
	writeJSON(w, 200, s.withCost(r, d))
}

func (s *server) listConversations(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ListConversations(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) postMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Orch.PostUserMessage(r.Context(), chi.URLParam(r, "id"), body.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *server) listApprovals(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ListApprovals(r.Context(), s.org(r), r.URL.Query().Get("status"))
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range v {
		if v[i].Status == domain.ApprovalPending {
			v[i].Context = s.approvalContext(v[i].ID)
		}
	}
	writeJSON(w, 200, list(v))
}

func (s *server) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	ap, err := s.Approvals.Decide(r.Context(), chi.URLParam(r, "id"), body.Decision, body.Note)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ap)
}

func (s *server) listReports(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ListReports(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) getReport(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.GetReport(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) activity(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 1000)
	}
	v, err := s.Store.ListActivity(r.Context(), s.org(r), "", limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) metrics(w http.ResponseWriter, r *http.Request) {
	m, err := s.Queries.Metrics(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) reset(w http.ResponseWriter, r *http.Request) {
	if err := s.Orch.Reset(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
