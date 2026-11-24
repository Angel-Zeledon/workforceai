package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
	"aiworkforce/backend/internal/projects"
)

// Company goals (F1, docs/architecture/goals.md): GET/POST /goals, GET/PATCH /goals/{id}. The goals service decides
// who sees a goal (D3: a private department's goal only its members) and who writes it (a company goal the
// administrators, a department's goal its responsibles or an administrator). The route only asks for the usual
// read (requests:read) or write (requests:create) permission.
func (s *server) mountGoals(r chi.Router) {
	if s.Goals == nil {
		return
	}
	read, write := s.can(auth.PermRequestsRead), s.can(auth.PermRequestsCreate)
	r.With(read).Get("/goals", s.listGoals)
	r.With(write).Post("/goals", s.createGoal)
	r.With(read).Get("/goals/{id}", s.getGoal)
	r.With(write).Patch("/goals/{id}", s.updateGoal)
}

func (s *server) goalActor(r *http.Request) goals.Actor {
	a := goals.Actor{ID: s.actor(r), Admin: true, Open: !s.AuthEnabled}
	if s.AuthEnabled {
		p, ok := auth.PrincipalFrom(r.Context())
		a.ID, a.Admin = p.UserID, ok && p.Role.Has(auth.PermOrgManage)
	}
	return a
}

// goalRef is the "contributes to" of a request or report.
type goalRef struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	OfficeName string `json:"office_name,omitempty"`
}

// goalOf returns the goal of a piece of work when the person sees it (nil: none, hidden or goals off).
func (s *server) goalOf(r *http.Request, goalID string) *goalRef {
	if s.Goals == nil || goalID == "" {
		return nil
	}
	g, ok := s.Goals.Visible(r.Context(), s.org(r), s.goalActor(r), goalID)
	if !ok {
		return nil
	}
	return &goalRef{ID: g.ID, Title: g.Title, Status: g.Status, OfficeName: g.OfficeName}
}

// hideGoals blanks the goal of the requests whose goal the person may not see (it answers like no goal at all).
func (s *server) hideGoals(r *http.Request, reqs []domain.Request) {
	seen := map[string]bool{}
	for i := range reqs {
		id := reqs[i].GoalID
		if id == "" {
			continue
		}
		vis, ok := seen[id]
		if !ok {
			vis = s.goalOf(r, id) != nil
			seen[id] = vis
		}
		if !vis {
			reqs[i].GoalID = ""
		}
	}
}

func (s *server) listGoals(w http.ResponseWriter, r *http.Request) {
	l, err := s.Goals.List(r.Context(), s.org(r), s.goalActor(r), r.URL.Query().Get("include_archived") == "1")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"goals": l, "max_active": goals.MaxActive})
}

func (s *server) createGoal(w http.ResponseWriter, r *http.Request) {
	var body goals.Input
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	g, err := s.Goals.Create(r.Context(), s.org(r), s.goalActor(r), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) updateGoal(w http.ResponseWriter, r *http.Request) {
	var body goals.Patch
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	g, err := s.Goals.Update(r.Context(), s.org(r), s.goalActor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, g)
}

// goalRequest and goalProject are the work linked to a goal, as the goal page lists it.
type goalRequest struct {
	ID        string               `json:"id"`
	Text      string               `json:"text"`
	Status    domain.RequestStatus `json:"status"`
	ReportID  *string              `json:"report_id"`
	CreatedAt time.Time            `json:"created_at"`
}

type goalProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// getGoal is GET /goals/{id}: the goal and the work linked to it that the person sees (D3 applies to each piece).
func (s *server) getGoal(w http.ResponseWriter, r *http.Request) {
	g, err := s.Goals.Get(r.Context(), s.org(r), s.goalActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	reqs, err := s.Store.ListRequests(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	reqs = slices.DeleteFunc(reqs, func(x domain.Request) bool { return x.GoalID != g.ID })
	ws := s.work(r)
	if len(reqs) > 0 {
		if err := ws.indexAll(); err != nil {
			s.fail(w, err)
			return
		}
	}
	// a project's own request is listed under the project, not twice
	var prjs []goalProject
	own := map[string]bool{}
	if s.Projects != nil {
		ps, err := s.Projects.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, p := range ps {
			if p.GoalID != g.ID {
				continue
			}
			if p.RequestID != "" {
				own[p.RequestID] = true
			}
			if ok, err := s.projectSeen(r, ws, p); err != nil {
				s.fail(w, err)
				return
			} else if ok {
				prjs = append(prjs, goalProject{ID: p.ID, Name: p.Name, Status: p.Status})
			}
		}
	}
	out := []goalRequest{}
	for _, q := range reqs {
		if own[q.ID] || !ws.request(q.ID) {
			continue
		}
		out = append(out, goalRequest{ID: q.ID, Text: q.Text, Status: q.Status, ReportID: q.ReportID, CreatedAt: q.CreatedAt})
	}
	if ws.err != nil {
		s.fail(w, ws.err)
		return
	}
	writeJSON(w, 200, map[string]any{"goal": g, "requests": out, "projects": list(prjs)})
}

// projectSeen applies the D3 rule of GET /projects: a project that is only a private office's work is left out.
func (s *server) projectSeen(r *http.Request, ws *workScope, p projects.Summary) (bool, error) {
	if err := ws.load(); err != nil {
		return false, err
	}
	if ws.open() || !ws.hidesAny() {
		return true, nil
	}
	d, err := s.Projects.Get(r.Context(), p.ID)
	if err != nil {
		return false, err
	}
	return !redactProjectFor(&d, ws), nil
}

// reportView is a report with the goal its request contributed to ("Contribuye a: …").
type reportView struct {
	domain.Report
	Goal *goalRef `json:"goal,omitempty"`
}

// reportViews adds to each report the goal of its request, when the person sees it.
func (s *server) reportViews(r *http.Request, reps []domain.Report) []reportView {
	out := make([]reportView, len(reps))
	goalOfReq := map[string]string{}
	if s.Goals != nil && len(reps) == 1 {
		if q, err := s.Store.GetRequest(r.Context(), s.org(r), reps[0].RequestID); err == nil {
			goalOfReq[q.ID] = q.GoalID
		}
	} else if s.Goals != nil && len(reps) > 1 {
		if qs, err := s.Store.ListRequests(r.Context(), s.org(r)); err == nil {
			for _, q := range qs {
				goalOfReq[q.ID] = q.GoalID
			}
		}
	}
	refs := map[string]*goalRef{}
	for i, rep := range reps {
		out[i].Report = rep
		gid := goalOfReq[rep.RequestID]
		if gid == "" {
			continue
		}
		ref, ok := refs[gid]
		if !ok {
			ref = s.goalOf(r, gid)
			refs[gid] = ref
		}
		out[i].Goal = ref
	}
	return out
}
