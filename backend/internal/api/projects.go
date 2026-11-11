package api

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
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
	r.With(s.can(auth.PermTasksRead), s.workProject).Get("/projects/{id}", s.getProject)
	r.With(s.can(auth.PermTasksRead), s.workProject).Get("/projects/{id}/snapshot", s.getProject)
	r.With(s.can(auth.PermTasksRead), s.workProject).Get("/projects/{id}/health", s.projectHealth)
	r.With(s.can(auth.PermApprovalsRead), s.workProject).Get("/projects/{id}/approvals", s.projectApprovals)
	r.With(s.can(auth.PermRequestsCreate), s.workProject).Patch("/projects/{id}/plan", s.patchProjectPlan)
	r.With(s.can(auth.PermRequestsCreate), s.workProject).Post("/projects/{id}/estimate", s.estimateProject)
	r.With(s.can(auth.PermRequestsCreate), s.workProject).Post("/projects/{id}/validate", s.validateProject)
	r.With(s.can(auth.PermRequestsCreate), s.workProject).Post("/projects/{id}/launch", s.launchProject)
	// Running projects spend money and hold agents: pausing, cancelling and the budget are admin actions.
	r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/control", s.controlProject)
	for _, a := range []string{"pause", "resume", "cancel"} {
		a := a
		r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/"+a, func(w http.ResponseWriter, r *http.Request) { s.projectAction(w, r, a) })
	}
	r.With(s.can(auth.PermTasksManage), s.workProject).Put("/projects/{id}/budget", s.projectBudget)
	// W2 manual recovery of a failed/blocked node (human-only, audited).
	r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/nodes/{nodeId}/retry", s.retryProjectNode)
	r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/nodes/{nodeId}/skip", s.skipProjectNode)
	// Q2 mid-flight replanning: agents only propose; a human decides.
	r.With(s.can(auth.PermTasksRead), s.workProject).Get("/projects/{id}/plan-changes", s.listPlanChanges)
	r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/plan-changes", s.createPlanChange)
	r.With(s.can(auth.PermApprovalsDecide), s.workProject).Post("/projects/{id}/plan-changes/{changeId}/approve", func(w http.ResponseWriter, r *http.Request) { s.decidePlanChange(w, r, "approve") })
	r.With(s.can(auth.PermApprovalsDecide), s.workProject).Post("/projects/{id}/plan-changes/{changeId}/reject", func(w http.ResponseWriter, r *http.Request) { s.decidePlanChange(w, r, "reject") })
	r.With(s.can(auth.PermTasksManage), s.workProject).Post("/projects/{id}/nodes/{nodeId}/replan", s.replanProjectNode)
	r.With(s.can(auth.PermRequestsCreate), s.workProject).Post("/projects/{id}/save-as-template", s.saveProjectTemplate)
	// Batches: one template for many customers (a project per customer, launched at the plan's pace).
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-batches/preview", s.previewBatch)
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-batches", s.createBatch)
	r.With(s.can(auth.PermTasksRead)).Get("/project-batches", s.listBatches)
	r.With(s.can(auth.PermTasksRead)).Get("/project-batches/{id}", s.getBatch)
	r.With(s.can(auth.PermTasksManage), s.workBatch).Post("/project-batches/{id}/control", s.controlBatch)
	r.With(s.can(auth.PermTasksManage), s.workBatch).Post("/project-batches/{id}/customers/{customerId}/cancel", s.cancelBatchCustomer)
	r.With(s.can(auth.PermTasksManage), s.workBatch).Post("/project-batches/{id}/retry-failed", s.retryBatchFailed)
	r.With(s.can(auth.PermTasksRead)).Get("/project-templates", s.listProjectTemplates)
	// Customizable work templates (G2): members create and edit, admins delete.
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-templates", s.createProjectTemplate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-templates/preview", s.previewProjectTemplate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-templates/validate", s.validateProjectTemplate)
	r.With(s.can(auth.PermTasksRead)).Get("/project-templates/{id}", s.getProjectTemplate)
	r.With(s.can(auth.PermRequestsCreate)).Put("/project-templates/{id}", s.updateProjectTemplate)
	r.With(s.can(auth.PermTasksManage)).Delete("/project-templates/{id}", s.deleteProjectTemplate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-templates/{id}/duplicate", s.duplicateProjectTemplate)
	r.With(s.can(auth.PermTasksRead)).Get("/project-templates/{id}/versions", s.projectTemplateVersions)
	r.With(s.can(auth.PermRequestsCreate)).Post("/project-templates/{id}/versions/{version}/restore", s.restoreProjectTemplate)
	// Deciding approvals is privileged (admin/owner); every decision still goes through Approvals.Decide.
	r.With(s.can(auth.PermApprovalsDecide)).Post("/approvals/batch", s.decideApprovalsBatch)
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	if !ws.open() && ws.hidesAny() { // D3: a project that is only a private office's work is left out (work_projects.go)
		out := v[:0]
		for _, p := range v {
			d, err := s.Projects.Get(r.Context(), p.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			if !redactProject(&d, ws.agent) {
				out = append(out, p)
			}
		}
		v = out
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
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	if !ws.open() && redactProject(&d, ws.agent) { // D3 (work_projects.go)
		s.fail(w, notFound("project", d.Project.ID))
		return
	}
	s.projectDeciders(r, d.Approvals) // D3: who may decide each pending one
	writeJSON(w, 200, d)
}

func (s *server) projectHealth(w http.ResponseWriter, r *http.Request) {
	h, err := s.Projects.Health(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	pv, _, err := s.projectView(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	pv.redactHealth(&h) // D3
	writeJSON(w, 200, h)
}

func (s *server) projectApprovals(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.Approvals(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	v = slices.DeleteFunc(v, func(a projects.Approval) bool { return a.AgentID != nil && !ws.agent(*a.AgentID) }) // D3
	s.projectDeciders(r, v)
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
	pv, _, err := s.projectView(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, op := range body.Ops { // D3: a hidden node answers like a missing one
		if pv.hidden[op.ID] {
			s.fail(w, notFound("node", op.ID))
			return
		}
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
	if ws := s.work(r); !ws.open() { // D3: the cost lines of hidden agents
		e.ByAgent = slices.DeleteFunc(e.ByAgent, func(x projects.EstAgent) bool { return x.AgentID != "" && !ws.agent(x.AgentID) })
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
	if s.Drive != nil {
		s.syncInputs(r, chi.URLParam(r, "id")) // a file deleted by hand no longer counts as provided
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

func (s *server) retryProjectNode(w http.ResponseWriter, r *http.Request) {
	res, err := s.Projects.RetryNode(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "node": res.Node, "project": res.Project, "requeued": list(res.Requeued)})
}

func (s *server) skipProjectNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Projects.SkipNode(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeId"), body.Reason)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "node": res.Node, "project": res.Project, "requeued": list(res.Requeued)})
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
	var body projects.SaveOpts
	if r.ContentLength != 0 { // the body is optional: name, description, category, shared
		if err := s.decode(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
			s.fail(w, err)
			return
		}
	}
	if err := s.checkProjectTemplate(r, chi.URLParam(r, "id")); err != nil { // D3: a template copies every node whole
		s.fail(w, err)
		return
	}
	t, err := s.Projects.SaveAsTemplateWith(r.Context(), chi.URLParam(r, "id"), body)
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
	// D3: the same rule as /projects/{id}: a project of hidden work answers like a missing one, and the approvals of
	// agents the person does not see are neither counted nor decided
	pv, ok, err := s.projectView(r, body.Filter.ProjectID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ok && pv.all {
		s.fail(w, notFound("project", body.Filter.ProjectID))
		return
	}
	if !pv.ws.open() {
		body.Visible = func(a domain.Approval) bool { return pv.ws.agent(a.AgentID) }
	}
	res, err := s.Projects.DecideBatch(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *server) listPlanChanges(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.ListChanges(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	pv, _, err := s.projectView(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	v = slices.DeleteFunc(v, pv.changeHidden) // D3
	writeJSON(w, 200, list(v))
}

func (s *server) createPlanChange(w http.ResponseWriter, r *http.Request) {
	var body projects.NewPlanChange
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	pv, _, err := s.projectView(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if pv.opsHidden(body.Ops) { // D3: a hidden node (or agent) answers like a missing one
		s.fail(w, notFound("node", ""))
		return
	}
	c, err := s.Projects.ProposeChange(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) decidePlanChange(w http.ResponseWriter, r *http.Request, decision string) {
	var body struct {
		Note string `json:"note"`
	}
	_ = s.decode(w, r, &body)
	pv, _, err := s.projectView(r, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if pv.hidden != nil { // D3: a change of the hidden part answers like a missing one
		cs, err := s.Projects.ListChanges(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, x := range cs {
			if x.ID == chi.URLParam(r, "changeId") && pv.changeHidden(x) {
				s.fail(w, notFound("plan change", x.ID))
				return
			}
		}
	}
	c, err := s.Projects.DecideChange(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "changeId"), decision, body.Note)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) replanProjectNode(w http.ResponseWriter, r *http.Request) {
	c, err := s.Projects.ProposeReplan(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) createProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var body projects.TemplateInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	t, err := s.Projects.CreateTemplate(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *server) getProjectTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := s.Projects.GetTemplate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *server) updateProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var body projects.TemplateInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	t, err := s.Projects.UpdateTemplate(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *server) deleteProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if err := s.Projects.DeleteTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) duplicateProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
			s.fail(w, err)
			return
		}
	}
	t, err := s.Projects.DuplicateTemplate(r.Context(), chi.URLParam(r, "id"), body.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *server) projectTemplateVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.Projects.TemplateVersions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) restoreProjectTemplate(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || n < 1 {
		s.fail(w, errInvalid("version must be a positive integer"))
		return
	}
	t, err := s.Projects.RestoreTemplate(r.Context(), chi.URLParam(r, "id"), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *server) validateProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var body projects.TemplateInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.Projects.ValidateTemplate(r.Context(), body))
}

func (s *server) previewProjectTemplate(w http.ResponseWriter, r *http.Request) {
	var body projects.PreviewRequest
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Projects.PreviewTemplate(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

// ---- batches ----

func (s *server) previewBatch(w http.ResponseWriter, r *http.Request) {
	var in projects.BatchInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Projects.PreviewBatch(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) createBatch(w http.ResponseWriter, r *http.Request) {
	var in projects.BatchInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.Projects.CreateBatch(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *server) listBatches(w http.ResponseWriter, r *http.Request) {
	list, err := s.Projects.ListBatches(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	if !ws.open() && ws.hidesAny() { // D3: a batch that is only a private department's work is left out
		out := list[:0]
		for _, b := range list {
			v, err := s.Projects.BatchView(r.Context(), b.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			ok, err := s.batchVisible(r, ws, &v)
			if err != nil {
				s.fail(w, err)
				return
			}
			if ok {
				out = append(out, b)
			}
		}
		list = out
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *server) getBatch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Projects.BatchView(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	ws := s.work(r)
	ok, err := s.batchVisible(r, ws, &v)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.fail(w, notFound("batch", id))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) controlBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.Projects.BatchControl(r.Context(), chi.URLParam(r, "id"), body.Action)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *server) cancelBatchCustomer(w http.ResponseWriter, r *http.Request) {
	b, err := s.Projects.CancelBatchCustomer(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "customerId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *server) retryBatchFailed(w http.ResponseWriter, r *http.Request) {
	n, err := s.Projects.RetryBatchFailed(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retried": n})
}
