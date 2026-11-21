package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountWatchdog registers the stalled-work watchdog (F6, docs/architecture/watchdog.md):
//
//	GET  /stalled-work                tasks:read       the stalled tasks the person sees (D3)
//	POST /stalled-work/scan           org:manage       run the sweep now (announce + suggest)
//	GET  /tasks/{id}/stall            tasks:read       is it stalled, who may take it, what a cancellation stops
//	POST /tasks/{id}/stall/retry      requests:create  re-run it through the normal path
//	POST /tasks/{id}/stall/reassign   tasks:manage     {agent_id}: an agent of the same department and role
//	POST /tasks/{id}/stall/cancel     tasks:manage     {confirm_task_ids}: exactly the active dependents shown
//
// Only people act (an agent never holds these permissions and the orchestrator refuses agent principals). The work of
// a private department is hidden like everywhere (404); a cancellation that would stop work the person does not see
// is refused. No-op when Deps.Watchdog is nil.
func (s *server) mountWatchdog(r chi.Router) {
	if s.Watchdog == nil {
		return
	}
	r.With(s.can(auth.PermTasksRead)).Get("/stalled-work", s.listStalled)
	r.With(s.can(auth.PermOrgManage)).Post("/stalled-work/scan", s.scanStalled)
	r.With(s.can(auth.PermTasksRead), s.workTask).Get("/tasks/{id}/stall", s.getStall)
	r.With(s.can(auth.PermRequestsCreate), s.workTask).Post("/tasks/{id}/stall/retry", func(w http.ResponseWriter, r *http.Request) {
		s.recoverStall(w, r, application.StallRetry)
	})
	r.With(s.can(auth.PermTasksManage), s.workTask).Post("/tasks/{id}/stall/reassign", func(w http.ResponseWriter, r *http.Request) {
		s.recoverStall(w, r, application.StallReassign)
	})
	r.With(s.can(auth.PermTasksManage), s.workTask).Post("/tasks/{id}/stall/cancel", func(w http.ResponseWriter, r *http.Request) {
		s.recoverStall(w, r, application.StallCancel)
	})
}

func (s *server) listStalled(w http.ResponseWriter, r *http.Request) {
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Watchdog.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	v = slices.DeleteFunc(v, func(t application.StalledTask) bool { return !ws.agent(t.AgentID) })
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) scanStalled(w http.ResponseWriter, r *http.Request) {
	res, err := s.Watchdog.Scan(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	raised := 0
	for _, x := range res {
		if x.Raised {
			raised++
		}
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "watchdog.scanned", Entity: "watchdog", EntityID: "scan",
			Details: map[string]any{"stalled": len(res), "raised": raised}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res, "raised": raised})
}

// stallView is GET /tasks/{id}/stall for one person: candidates and cascade limited to what they see.
type stallView struct {
	application.StallDetail
	// HiddenCascade counts dependents of departments the person does not see: a cancellation is then refused.
	HiddenCascade int `json:"hidden_cascade"`
}

func (s *server) stallFor(r *http.Request) (stallView, error) {
	d, err := s.Watchdog.Detail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return stallView{}, err
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		return stallView{}, err
	}
	v := stallView{StallDetail: d}
	v.Candidates = slices.DeleteFunc(slices.Clone(d.Candidates), func(c application.Candidate) bool { return !ws.agent(c.ID) })
	v.Cascade = slices.DeleteFunc(slices.Clone(d.Cascade), func(c application.CascadeTask) bool { return !ws.agent(c.AgentID) })
	v.HiddenCascade = len(d.Cascade) - len(v.Cascade)
	return v, nil
}

func (s *server) getStall(w http.ResponseWriter, r *http.Request) {
	v, err := s.stallFor(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) recoverStall(w http.ResponseWriter, r *http.Request, action string) {
	var body struct {
		AgentID        string   `json:"agent_id"`
		ConfirmTaskIDs []string `json:"confirm_task_ids"`
	}
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &body); err != nil {
			s.fail(w, err)
			return
		}
	}
	in := application.StallRecoverInput{Action: action, AgentID: strings.TrimSpace(body.AgentID), ConfirmTaskIDs: body.ConfirmTaskIDs}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	switch action {
	case application.StallReassign:
		if in.AgentID == "" || !ws.agent(in.AgentID) {
			s.fail(w, fmt.Errorf("%w: unknown agent", domain.ErrInvalid)) // D3: an agent whose work one does not see is no target
			return
		}
	case application.StallCancel:
		v, err := s.stallFor(r)
		if err != nil {
			s.fail(w, err)
			return
		}
		if v.HiddenCascade > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "the cancellation would also stop work of a department you are not in",
				"code": "cascade_hidden", "hidden_cascade": v.HiddenCascade})
			return
		}
	}
	res, err := s.Watchdog.Recover(r.Context(), chi.URLParam(r, "id"), in)
	if cascade, ok := application.IsCascadeChanged(err); ok {
		if cascade == nil {
			cascade = []application.CascadeTask{}
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "cascade_changed", "cancel_cascade": cascade})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}
