package api

import (
	"cmp"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rituals"
)

// The other paths to a department's work (D3 final review, C1): questions, plan reviews, rehearsals, artifacts,
// customers, rituals, debates, initiatives and standing approvals follow the same rule as requests and tasks (work.go):
// what the person may not see is left out of the listings and answers 404 by id, also for the actions on it.

// guardWork guards the routes about the item {param}: sees reports whether the person sees it. A missing item
// (domain.ErrNotFound) is left to the handler; any other error fails closed.
func (s *server) guardWork(kind, param string, sees func(r *http.Request, ws *workScope, id string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, id := s.work(r), chi.URLParam(r, param)
			if err := ws.load(); err != nil {
				s.fail(w, err)
				return
			}
			if !ws.open() {
				ok, err := sees(r, ws, id)
				if err != nil && !errors.Is(err, domain.ErrNotFound) {
					s.fail(w, err)
					return
				}
				if err == nil && !ok {
					s.fail(w, cmp.Or(ws.err, notFound(kind, id)))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// viewer reports whether userID is the person of the request.
func (ws *workScope) viewer(userID string) bool {
	return ws.load() == nil && ws.m != nil && ws.v.UserID != "" && userID == ws.v.UserID
}

// seesQuestion: the work of the agent that asks, or the question is for the person (they launched the work).
func (ws *workScope) seesQuestion(q application.Question) bool {
	return ws.agent(q.AgentID) || ws.viewer(q.AskedFor)
}

// seesRehearsal: the work of the agent, or the request it belongs to (its requester sees its rehearsal report).
func (ws *workScope) seesRehearsal(a domain.RehearsalAction) bool {
	return ws.agent(a.AgentID) || a.RequestID != "" && ws.request(a.RequestID)
}

// seesDebate: a request's debate is its request's; another one needs the work of every participant, unless the
// person held it.
func (ws *workScope) seesDebate(d domain.Debate) bool {
	if d.RequestID != "" {
		return ws.request(d.RequestID)
	}
	if ws.viewer(d.CreatedBy) {
		return true
	}
	for _, p := range d.Participants {
		if !ws.agent(p) {
			return false
		}
	}
	return true
}

// agentOK reports whether an optional agent id ("" = none) is one whose work the person sees.
func (ws *workScope) agentOK(id string) bool { return id == "" || ws.agent(id) }

// artifactRule is the rule of redactWorkspace for any artifact: hidden when written by an agent whose work the person
// may not see, made for a task of such an agent, or part of a project that is only such work.
type artifactRule struct {
	s        *server
	r        *http.Request
	ws       *workScope
	tasks    map[string]string // task -> agent (loaded once)
	projects map[string]bool   // project -> every agent node hidden
	err      error
}

func (s *server) artifactRule(r *http.Request, ws *workScope) *artifactRule {
	return &artifactRule{s: s, r: r, ws: ws, projects: map[string]bool{}}
}

func (a *artifactRule) sees(m artifacts.Meta) bool {
	if a.ws.open() {
		return true
	}
	if a.ws.err != nil {
		return false
	}
	if m.CreatedBy.Kind == artifacts.ActorAgent && !a.ws.agent(m.CreatedBy.ID) {
		return false
	}
	if m.TaskID != nil && *m.TaskID != "" {
		if a.tasks == nil {
			ts, err := a.s.Store.ListTasks(a.r.Context(), a.ws.org, "", "")
			if err != nil {
				a.err = err
				return false
			}
			a.tasks = make(map[string]string, len(ts))
			for _, t := range ts {
				a.tasks[t.ID] = t.AgentID
			}
		}
		if agent, ok := a.tasks[*m.TaskID]; ok && !a.ws.agent(agent) {
			return false
		}
	}
	if m.ProjectID != nil && *m.ProjectID != "" && a.s.Projects != nil {
		hidden, ok := a.projects[*m.ProjectID]
		if !ok {
			pv, found, err := a.s.projectView(a.r, *m.ProjectID)
			if err != nil {
				a.err = err
				return false
			}
			hidden = found && pv.all
			a.projects[*m.ProjectID] = hidden
		}
		if hidden {
			return false
		}
	}
	return true
}

// artifactVisible reports whether the person sees the artifact id (a missing one: domain.ErrNotFound).
func (s *server) artifactVisible(r *http.Request, ws *workScope, id string) (bool, error) {
	a, err := s.Artifacts.Get(r.Context(), id, 0)
	if err != nil {
		return false, err
	}
	rule := s.artifactRule(r, ws)
	ok := rule.sees(a.Meta)
	return ok, rule.err
}

// The guards of the routes by id.

func (s *server) workQuestion(next http.Handler) http.Handler {
	return s.guardWork("question", "id", func(r *http.Request, ws *workScope, id string) (bool, error) {
		q, err := s.Orch.Questions().Store.GetQuestion(r.Context(), ws.org, id)
		return err == nil && ws.seesQuestion(q), err
	})(next)
}

func (s *server) workRehearsal(next http.Handler) http.Handler {
	return s.guardWork("rehearsal action", "id", func(r *http.Request, ws *workScope, id string) (bool, error) {
		a, err := s.Rehearsal.Store.GetRehearsalAction(r.Context(), ws.org, id)
		return err == nil && ws.seesRehearsal(a), err
	})(next)
}

func (s *server) workArtifact(next http.Handler) http.Handler {
	return s.guardWork("artifact", "id", s.artifactVisible)(next)
}

func (s *server) workDebate(next http.Handler) http.Handler {
	return s.guardWork("debate", "id", func(r *http.Request, ws *workScope, id string) (bool, error) {
		d, err := s.Debates.Get(r.Context(), id)
		return err == nil && ws.seesDebate(d), err
	})(next)
}

func (s *server) workInitiative(next http.Handler) http.Handler {
	return s.guardWork("initiative", "id", func(r *http.Request, ws *workScope, id string) (bool, error) {
		it, err := s.Initiative.Store.GetInitiative(r.Context(), ws.org, id)
		return err == nil && ws.agentOK(it.AgentID), err
	})(next)
}

func (s *server) workRitualProposal(next http.Handler) http.Handler {
	return s.guardWork("proposal", "id", func(r *http.Request, ws *workScope, id string) (bool, error) {
		p, err := s.Rituals.Proposal(r.Context(), id)
		return err == nil && ws.agentOK(p.AgentID), err
	})(next)
}

// workStanding guards revoking the standing approval {sid}: one of an agent whose work the person may not see.
func (s *server) workStanding(next http.Handler) http.Handler {
	return s.guardWork("standing approval", "sid", func(r *http.Request, ws *workScope, id string) (bool, error) {
		sa, err := s.Standing.Store.GetStanding(r.Context(), ws.org, id)
		return err == nil && ws.agentOK(sa.AgentID), err
	})(next)
}

// redactRitual removes what the person may not see from a ritual (rituals.Redact). A retro read without its
// proposals counts the visible ones for the rewritten summary.
func (s *server) redactRitual(r *http.Request, ws *workScope, rt *rituals.Ritual) error {
	if ws.open() {
		return nil
	}
	if ws.err != nil {
		return ws.err
	}
	hidden := false
	for _, e := range rt.Entries {
		hidden = hidden || !ws.agentOK(e.AgentID)
	}
	for _, a := range rt.Absent {
		hidden = hidden || !ws.agentOK(a.AgentID)
	}
	for _, p := range rt.Proposals {
		hidden = hidden || !ws.agentOK(p.AgentID)
	}
	if !hidden {
		return nil
	}
	props := rt.Proposals
	if rt.Kind == rituals.KindRetro && props == nil {
		ps, err := s.Rituals.Proposals(r.Context(), rituals.ProposalQuery{RitualID: rt.ID, Limit: 200})
		if err != nil {
			return err
		}
		props = ps
	}
	n := 0
	for _, p := range props {
		if ws.agentOK(p.AgentID) {
			n++
		}
	}
	rituals.Redact(rt, ws.agentOK, n)
	return nil
}
