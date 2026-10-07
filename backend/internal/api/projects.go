package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/projects"
)

// Projects API (additive, v1): docs/architecture/workflow-visualization.md
// sec. 7.4 and its Appendix A; docs/architecture/08-api.md. A project is a
// whole workflow with parallel work; launching it creates real tasks.

func (s *server) mountProjects(r chi.Router) {
	if s.Projects == nil {
		return
	}
	r.With(s.can(auth.PermTasksRead)).Get("/projects", s.listProjects)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/draft", s.createProjectDraft)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/from-template", s.createProjectDraft)
	r.With(s.can(auth.PermTasksRead)).Get("/projects/{id}", s.getProject)
	r.With(s.can(auth.PermTasksRead)).Get("/projects/{id}/snapshot", s.getProject)
	r.With(s.can(auth.PermTasksRead)).Get("/projects/{id}/health", s.projectHealth)
	r.With(s.can(auth.PermApprovalsRead)).Get("/projects/{id}/approvals", s.projectApprovals)
	r.With(s.can(auth.PermRequestsCreate)).Patch("/projects/{id}/plan", s.patchProjectPlan)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/{id}/estimate", s.estimateProject)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/{id}/validate", s.validateProject)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/{id}/launch", s.launchProject)
	// Running projects spend money and hold agents: pausing, cancelling and the budget are admin actions.
	r.With(s.can(auth.PermTasksManage)).Post("/projects/{id}/control", s.controlProject)
	for _, a := range []string{"pause", "resume", "cancel"} {
		a := a
		r.With(s.can(auth.PermTasksManage)).Post("/projects/{id}/"+a, func(w http.ResponseWriter, r *http.Request) { s.projectAction(w, r, a) })
	}
	r.With(s.can(auth.PermTasksManage)).Put("/projects/{id}/budget", s.projectBudget)
	r.With(s.can(auth.PermRequestsCreate)).Post("/projects/{id}/save-as-template", s.saveProjectTemplate)
	r.With(s.can(auth.PermTasksRead)).Get("/project-templates", s.listProjectTemplates)
	// Deciding approvals is privileged (admin/owner); every decision still goes through Approvals.Decide.
	r.With(s.can(auth.PermApprovalsDecide)).Post("/approvals/batch", s.decideApprovalsBatch)
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) createProjectDraft(w http.ResponseWriter, r *http.Request) {
	var body projects.NewProject
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/from-template") && body.TemplateID == "" {
		s.fail(w, errInvalid("template_id is required"))
		return
	}
	rec, err := s.Projects.CreateDraft(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"project_id": rec.ID})
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request) {
	d, err := s.Projects.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *server) projectHealth(w http.ResponseWriter, r *http.Request) {
	h, err := s.Projects.Health(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, h)
}

func (s *server) projectApprovals(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.Approvals(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) patchProjectPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ops []projects.PlanOp `json:"ops"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	ver, issues, err := s.Projects.PatchPlan(r.Context(), chi.URLParam(r, "id"), body.Ops)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"structure_version": ver, "issues": issues})
}

func (s *server) estimateProject(w http.ResponseWriter, r *http.Request) {
	e, err := s.Projects.Estimate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"estimate": e})
}

func (s *server) validateProject(w http.ResponseWriter, r *http.Request) {
	issues, err := s.Projects.Validate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ok := true
	for _, i := range issues {
		ok = ok && i.Severity != "error"
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "issues": list(issues)})
}

func (s *server) launchProject(w http.ResponseWriter, r *http.Request) {
	var body projects.LaunchBody
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Projects.Launch(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "project": p})
}

func (s *server) controlProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	s.runControl(w, r, body.Action)
}

func (s *server) projectAction(w http.ResponseWriter, r *http.Request, action string) {
	s.runControl(w, r, action)
}

func (s *server) runControl(w http.ResponseWriter, r *http.Request, action string) {
	p, err := s.Projects.Control(r.Context(), chi.URLParam(r, "id"), action)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": p.Status, "control": p.Control, "project": p})
}

func (s *server) projectBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Projects.SetBudget(r.Context(), chi.URLParam(r, "id"), body.BudgetUSD)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "project": p})
}

func (s *server) saveProjectTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := s.Projects.SaveAsTemplate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *server) listProjectTemplates(w http.ResponseWriter, r *http.Request) {
	t, err := s.Projects.Templates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(t))
}

func (s *server) decideApprovalsBatch(w http.ResponseWriter, r *http.Request) {
	var body projects.BatchDecision
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Projects.DecideBatch(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}
