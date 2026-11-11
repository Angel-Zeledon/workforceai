package api

import (
	"cmp"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// "Necesita tu ayuda" (docs/architecture/approvals-inbox.md, questions; 08-api.md §16.9): the questions agents ask
// people mid-task. Reading needs tasks:read; answering needs tasks:create and being the person the question is for,
// or tasks:manage (admins answer any). Agents never answer. D3: a question of an agent whose work the person may not
// see is left out and answers 404 (work_sealed.go), unless it is for them.
func (s *server) mountQuestions(r chi.Router) {
	if s.Orch == nil || s.Orch.Questions() == nil {
		return
	}
	r.With(s.can(auth.PermTasksRead)).Get("/questions", s.listQuestions)
	r.With(s.can(auth.PermTasksRead), s.workQuestion).Get("/questions/{id}", s.getQuestion)
	r.With(s.can(auth.PermTasksCreate), s.workQuestion).Post("/questions/{id}/answer", s.answerQuestion)
	r.With(s.can(auth.PermTasksCreate)).Post("/questions/answer-batch", s.answerQuestions)
}

// QuestionItem is one question with what the inbox card needs.
type QuestionItem struct {
	application.Question
	TaskTitle      string `json:"task_title,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	BatchName      string `json:"batch_name,omitempty"`
	WaitingSeconds int    `json:"waiting_seconds"`
	ForMe          bool   `json:"for_me"`
	CanAnswer      bool   `json:"can_answer"`
}

func (s *server) questionItem(r *http.Request, q application.Question, names func(string) string) QuestionItem {
	ctx, org := r.Context(), s.org(r)
	it := QuestionItem{Question: q, WaitingSeconds: int(time.Since(q.CreatedAt).Seconds())}
	if q.Status != application.QuestionOpen && q.ResolvedAt != nil {
		it.WaitingSeconds = int(q.ResolvedAt.Sub(q.CreatedAt).Seconds())
	}
	if t, err := s.Store.GetTask(ctx, org, q.TaskID); err == nil {
		it.TaskTitle = t.Title
	}
	if q.ProjectID != "" && names != nil {
		it.ProjectName = names(q.ProjectID)
	}
	if id, ok := strings.CutPrefix(q.GroupKey, "batch:"); ok && s.Projects != nil {
		it.BatchName = s.Projects.BatchName(ctx, id)
	}
	uid, manage := s.answerer(r)
	it.ForMe = q.AskedFor != "" && q.AskedFor == uid
	it.CanAnswer = q.Status == application.QuestionOpen && (manage || it.ForMe || (q.AskedFor == "" && !s.AuthEnabled))
	return it
}

// answerer is who is reading: their user id and whether they may answer any question.
func (s *server) answerer(r *http.Request) (string, bool) {
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		return p.UserID, p.Can(auth.PermTasksManage)
	}
	return "demo", !s.AuthEnabled
}

func (s *server) projectNamer(r *http.Request) func(string) string {
	if s.Projects == nil {
		return nil
	}
	var names map[string]string
	return func(id string) string {
		if names == nil {
			names = projectNames(r.Context(), s)
		}
		return names[id]
	}
}

// listQuestions: GET /questions?status=open (default) | answered | ... | all, newest first.
func (s *server) listQuestions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "":
		status = application.QuestionOpen
	case "all":
		status = ""
	case application.QuestionOpen, application.QuestionAnswered, application.QuestionReassigned, application.QuestionSkipped, application.QuestionCancelled:
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown status"})
		return
	}
	qs, err := s.Orch.Questions().Store.ListQuestions(r.Context(), s.org(r), status)
	if err != nil {
		s.fail(w, err)
		return
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	names := s.projectNamer(r)
	items := make([]QuestionItem, 0, len(qs))
	for _, q := range qs {
		if !ws.open() && !ws.seesQuestion(q) {
			continue
		}
		items = append(items, s.questionItem(r, q, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// getQuestion: GET /questions/{id}: the question, what skipping it would leave undone and who could take the task.
func (s *server) getQuestion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q, err := s.Orch.Questions().Store.GetQuestion(r.Context(), s.org(r), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"question": s.questionItem(r, q, s.projectNamer(r))}
	if q.Status == application.QuestionOpen {
		if imp, err := s.Orch.QuestionImpact(r.Context(), id); err == nil {
			out["impact"] = imp
		}
		cands := []application.Candidate{}
		ws := s.work(r)
		if agents, err := s.Store.ListAgents(r.Context(), s.org(r)); err == nil {
			for _, a := range agents {
				// D3: the task would become the work of an agent the person may not see: not offered
				if a.ID != q.AgentID && a.Role != application.AuditorRole && ws.agent(a.ID) {
					cands = append(cands, application.Candidate{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
				}
			}
		}
		out["reassign_candidates"] = cands
	}
	writeJSON(w, http.StatusOK, out)
}

type answerBody struct {
	application.Resolution
	Confirm bool `json:"confirm"`
}

// mayAnswer: the person the question is for, or someone who may manage any task.
func (s *server) mayAnswer(r *http.Request, q application.Question) bool {
	uid, manage := s.answerer(r)
	return manage || (q.AskedFor != "" && q.AskedFor == uid)
}

// answerQuestion: POST /questions/{id}/answer {kind, text?, option?, file_path?, agent_id?, confirm?}.
// A skip without confirm, or one that would leave the work with no result, is 409 with the impact.
func (s *server) answerQuestion(w http.ResponseWriter, r *http.Request) {
	var body answerBody
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	q, err := s.Orch.Questions().Store.GetQuestion(r.Context(), s.org(r), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.mayAnswer(r, q) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the person this question is for (or an admin) can answer it"})
		return
	}
	if ws := s.work(r); body.Kind == application.AnswerReassign && body.AgentID != "" && !ws.agent(body.AgentID) {
		s.fail(w, cmp.Or(ws.err, notFound("agent", body.AgentID))) // D3: handing it to an agent whose work is hidden
		return
	}
	res, err := s.Orch.AnswerQuestion(r.Context(), id, body.Resolution, body.Confirm)
	if err != nil {
		if body.Kind == application.AnswerSkip && errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "skip_") {
			imp, _ := s.Orch.QuestionImpact(r.Context(), id)
			code := "skip_needs_confirm"
			if strings.Contains(err.Error(), "skip_blocks_result") {
				code = "skip_blocks_result"
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": code, "impact": imp})
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.questionItem(r, res, s.projectNamer(r)))
}

// answerQuestions: POST /questions/answer-batch {ids, kind: text|option, text?, option?}: the same answer for several
// questions ("3 customers have no phone: use the e-mail"). Each one is answered on its own; failures are reported.
func (s *server) answerQuestions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
		application.Resolution
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids: between 1 and 200 questions"})
		return
	}
	if body.Kind != application.AnswerText && body.Kind != application.AnswerOption {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a batch answer is text or option"})
		return
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	answered, failed := []string{}, map[string]string{}
	for _, id := range body.IDs {
		q, err := s.Orch.Questions().Store.GetQuestion(r.Context(), s.org(r), id)
		if err == nil && !ws.open() && !ws.seesQuestion(q) {
			err = domain.ErrNotFound // D3: a hidden question answers like a missing one
		}
		if err == nil && !s.mayAnswer(r, q) {
			err = domain.ErrForbidden
		}
		if err == nil {
			_, err = s.Orch.AnswerQuestion(r.Context(), id, body.Resolution, false)
		}
		if err != nil {
			failed[id] = err.Error()
			continue
		}
		answered = append(answered, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": answered, "failed": failed})
}
