package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/skills"
)

// Test a skill with a case before activating it (F8, docs/architecture/skills.md "Probar una habilidad", 08-api.md
// section 53). Reading cases and results is skills:read, saving or deleting cases is skills:write (the service also
// asks for the right on a department's skill), and RUNNING a test (it spends budget) is skills:review: the same
// permission that activates a skill. Agents have no identity here, so an agent can never start one; the service
// refuses system actors anyway. A skill of a private department is sealed from whoever cannot see it.

func (s *server) mountSkillTests(r chi.Router) {
	if s.SkillTests == nil {
		return
	}
	read, write, review := s.can(auth.PermSkillsRead), s.can(auth.PermSkillsWrite), s.can(auth.PermSkillsReview)
	r.With(read).Get("/skills/{id}/tests", s.skillTestsOverview)
	r.With(read).Get("/skills/{id}/tests/candidates", s.skillTestCandidates)
	r.With(read).Get("/skills/{id}/tests/cases/{caseId}/runs", s.skillTestCaseRuns)
	r.With(write).Post("/skills/{id}/tests/cases", s.saveSkillTestCase)
	r.With(write).Put("/skills/{id}/tests/cases/{caseId}", s.saveSkillTestCase)
	r.With(write).Delete("/skills/{id}/tests/cases/{caseId}", s.deleteSkillTestCase)
	r.With(review).Post("/skills/{id}/tests/estimate", s.estimateSkillTest)
	r.With(review).Post("/skills/{id}/tests/run", s.runSkillTest)
}

func (s *server) skillTestsOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.SkillTests.Overview(r.Context(), s.skillActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *server) skillTestCaseRuns(w http.ResponseWriter, r *http.Request) {
	l, err := s.SkillTests.Runs(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "caseId"), queryInt(r, "limit"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(l))
}

// skillTestAgent is an agent a person may pick to run a test.
type skillTestAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Title string `json:"title"`
}

// skillTestCandidates lists the agents that can use the skill AND that the person may use (a private
// department's agents are not offered to whoever cannot see them).
func (s *server) skillTestCandidates(w http.ResponseWriter, r *http.Request) {
	agents, err := s.SkillTests.Candidates(r.Context(), s.skillActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.skillFail(w, err)
		return
	}
	m, v, err := s.agentModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []skillTestAgent{}
	for _, a := range agents {
		if m != nil && !m.Rights(v, a.ID).Use {
			continue
		}
		out = append(out, skillTestAgent{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) saveSkillTestCase(w http.ResponseWriter, r *http.Request) {
	var in skills.CaseInput
	if !s.decodeSkill(w, r, &in) {
		return
	}
	c, err := s.SkillTests.SaveCase(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "caseId"), in)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, c)
}

func (s *server) deleteSkillTestCase(w http.ResponseWriter, r *http.Request) {
	if err := s.SkillTests.DeleteCase(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "caseId")); err != nil {
		s.skillFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// skillTestInput decodes a run request and checks that the person may use the agent they picked.
func (s *server) skillTestInput(w http.ResponseWriter, r *http.Request) (skills.RunInput, bool) {
	var in skills.RunInput
	if !s.decodeSkill(w, r, &in) {
		return in, false
	}
	if err := s.checkAgentUse(r, "agent:"+in.AgentID); err != nil {
		s.fail(w, err)
		return in, false
	}
	return in, true
}

func (s *server) estimateSkillTest(w http.ResponseWriter, r *http.Request) {
	in, ok := s.skillTestInput(w, r)
	if !ok {
		return
	}
	e, err := s.SkillTests.Estimate(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *server) runSkillTest(w http.ResponseWriter, r *http.Request) {
	in, ok := s.skillTestInput(w, r)
	if !ok {
		return
	}
	runs, err := s.SkillTests.Run(r.Context(), s.skillActor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.skillFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list(runs)})
}
