package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/sandbox"
)

// Code sandbox API (docs/architecture/code-sandbox.md, 08-api.md). The setting
// is owner/admin only (org:manage) and audited; the runs (they hold the code
// and its output) are for those who can decide approvals. Optional: no-op when
// Deps.Sandbox is nil.
func (s *server) mountCode(r chi.Router) {
	if s.Sandbox == nil {
		return
	}
	r.With(s.can(auth.PermOrgManage)).Get("/settings/code-execution", s.getCodeSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/code-execution", s.putCodeSettings)
	r.With(s.can(auth.PermApprovalsDecide)).Get("/code-runs", s.listCodeRuns)
	r.With(s.can(auth.PermApprovalsDecide)).Get("/code-runs/{id}", s.getCodeRun)
}

type codeSettingsView struct {
	Enabled           bool `json:"enabled"`
	MaxRunsPerDay     int  `json:"max_runs_per_day"`
	MaxRuntimeSeconds int  `json:"max_runtime_seconds"`
	// Available: a sandbox runner is configured on the server (without it
	// nothing runs even when enabled).
	Available bool `json:"available"`
	// Platform ceilings: the organization can only go lower.
	CeilingRunsPerDay     int `json:"ceiling_runs_per_day"`
	CeilingRuntimeSeconds int `json:"ceiling_runtime_seconds"`
}

func (s *server) codeSettingsView(in sandbox.Settings) codeSettingsView {
	ceil := s.Sandbox.Ceiling.Clamp(sandbox.DefaultLimits())
	perDay := s.Sandbox.MaxRunsPerDay
	if perDay <= 0 {
		perDay = sandbox.DefaultMaxRunsPerDay
	}
	return codeSettingsView{Enabled: in.Enabled, MaxRunsPerDay: in.MaxRunsPerDay, MaxRuntimeSeconds: in.MaxRuntimeSeconds,
		Available: s.SandboxAvailable, CeilingRunsPerDay: perDay, CeilingRuntimeSeconds: ceil.TimeoutSeconds}
}

func (s *server) getCodeSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Sandbox.Settings(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.codeSettingsView(v))
}

func (s *server) putCodeSettings(w http.ResponseWriter, r *http.Request) {
	var in sandbox.Settings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Sandbox.PutSettings(r.Context(), s.org(r), s.actor(r), sandbox.Settings{Enabled: in.Enabled,
		MaxRunsPerDay: in.MaxRunsPerDay, MaxRuntimeSeconds: in.MaxRuntimeSeconds})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.codeSettingsView(v))
}

// listCodeRuns lists the runs, newest first. ?approval_id= finds the run of an
// approval (what the approval card shows); ?limit= up to 500.
func (s *server) listCodeRuns(w http.ResponseWriter, r *http.Request) {
	org := s.org(r)
	if ap := r.URL.Query().Get("approval_id"); ap != "" {
		run, err := s.Sandbox.Store.GetRunByApproval(r.Context(), org, ap)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, []sandbox.Run{normalizeRun(run)})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Sandbox.Store.ListRuns(r.Context(), org, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]sandbox.Run, 0, len(runs))
	for _, run := range runs {
		out = append(out, normalizeRun(run))
	}
	runs = out
	writeJSON(w, http.StatusOK, runs)
}

func (s *server) getCodeRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.Sandbox.Store.GetRun(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizeRun(run))
}

// normalizeRun makes the lists JSON arrays (never null) so clients can iterate them.
func normalizeRun(r sandbox.Run) sandbox.Run {
	if r.Inputs == nil {
		r.Inputs = []sandbox.InputInfo{}
	}
	if r.Outputs == nil {
		r.Outputs = []sandbox.OutputInfo{}
	}
	if r.ExpectedOutputs == nil {
		r.ExpectedOutputs = []string{}
	}
	if r.Approvers == nil {
		r.Approvers = []string{}
	}
	return r
}
