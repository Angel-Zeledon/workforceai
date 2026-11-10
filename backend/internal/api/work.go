package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// The work of a private department (D3, equipos-oficinas.md §4.1): the requests, tasks, reports, activity, chats and
// memory of an agent of a private office are only its members', admins included. What is hidden answers exactly
// like what does not exist: left out of the listings, 404 in a detail.

// workScope is the rule for one HTTP request: the offices model is loaded once (lazily) and the visibility of each
// request is computed once.
type workScope struct {
	s      *server
	r      *http.Request
	loaded bool
	m      *offices.Model // nil: everything visible (no offices, no auth, nothing restricted)
	v      offices.Viewer
	err    error
	agents map[string][]string // request -> the agents of its tasks (filled by index or on demand)
	seen   map[string]bool     // request -> visible
}

func (s *server) work(r *http.Request) *workScope {
	return &workScope{s: s, r: r, agents: map[string][]string{}, seen: map[string]bool{}}
}

func (w *workScope) load() error {
	if !w.loaded {
		w.loaded = true
		w.m, w.v, w.err = w.s.agentModel(w.r)
	}
	return w.err
}

// open reports whether nothing is hidden for this person (no model to apply).
func (w *workScope) open() bool { return w.load() == nil && w.m == nil }

// agent reports whether the person sees the work of an agent (fails closed when the model cannot be read).
func (w *workScope) agent(id string) bool {
	if err := w.load(); err != nil {
		return false
	}
	return w.m == nil || w.m.CanSeeWork(w.v, id)
}

// index records the agents of the given tasks by request, so the visibility of their requests needs no more reads.
// The tasks must be every task of each request they name.
func (w *workScope) index(tasks []domain.Task) {
	for _, t := range tasks {
		if _, ok := w.agents[t.RequestID]; !ok {
			w.agents[t.RequestID] = []string{}
		}
		w.agents[t.RequestID] = append(w.agents[t.RequestID], t.AgentID)
	}
}

// indexAll indexes every task of the organization (for a listing), unless nothing is hidden.
func (w *workScope) indexAll() error {
	if err := w.load(); err != nil || w.m == nil {
		return err
	}
	tasks, err := w.s.Store.ListTasks(w.r.Context(), w.s.org(w.r), "", "")
	if err != nil {
		return err
	}
	w.index(tasks)
	return nil
}

// request reports whether the person sees a request: they see the work of every agent of its tasks, or they asked
// for it (whoever asked always sees their request). A request without tasks yet is visible.
func (w *workScope) request(id string) bool {
	if err := w.load(); err != nil {
		return false
	}
	if w.m == nil || id == "" {
		return true
	}
	if v, ok := w.seen[id]; ok {
		return v
	}
	agents, ok := w.agents[id]
	if !ok {
		tasks, err := w.s.Store.ListTasksByRequest(w.r.Context(), w.s.org(w.r), id)
		if err != nil {
			return false
		}
		w.index(tasks)
		agents = w.agents[id]
	}
	vis := true
	for _, a := range agents {
		if !w.m.CanSeeWork(w.v, a) {
			vis = false
			break
		}
	}
	if !vis && w.v.UserID != "" && w.s.Orch != nil && w.s.Orch.RequestedBy(w.r.Context(), id) == w.v.UserID {
		vis = true
	}
	w.seen[id] = vis
	return vis
}

// conversation reports whether the person sees a stored conversation: its agents and its request.
func (w *workScope) conversation(c domain.Conversation) bool {
	for _, p := range c.Participants {
		if !w.agent(p) {
			return false
		}
	}
	return c.RequestID == nil || w.request(*c.RequestID)
}

// conversationID reports whether the person sees a conversation by id: the agent of a chat (office, agent:<id>),
// else the stored conversation (a missing one is left to the caller's own not found).
func (w *workScope) conversationID(id string) (bool, error) {
	if application.IsChatConversation(id) {
		agent, ok := strings.CutPrefix(domain.PublicChatConv(id), domain.ChatAgentPrefix)
		return !ok || w.agent(agent), nil
	}
	if err := w.load(); err != nil || w.m == nil {
		return true, err
	}
	c, err := w.s.Store.GetConversation(w.r.Context(), w.s.org(w.r), id)
	if err != nil {
		return true, nil
	}
	return w.conversation(c), nil
}

// context returns the request's context carrying the rule for the application's reads (the chat history).
func (w *workScope) context() *http.Request {
	return w.r.WithContext(application.WithWorkFilter(w.r.Context(), application.WorkFilter{Agent: w.agent, Request: w.request}))
}

// seesWork reports whether the person of the request sees the work of an agent (true without offices).
func (s *server) seesWork(r *http.Request, agentID string) bool { return s.work(r).agent(agentID) }

// hideWork answers 404 for the work of an agent ({id}) the person may not see and reports whether it did.
func (s *server) hideWork(w http.ResponseWriter, r *http.Request) bool {
	id := chi.URLParam(r, "id")
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return true
	}
	if !ws.agent(id) {
		s.fail(w, fmt.Errorf("%w: agent %s", domain.ErrNotFound, id))
		return true
	}
	return false
}

// notFound is the answer to something hidden: the same as something missing.
func notFound(kind, id string) error { return fmt.Errorf("%w: %s %s", domain.ErrNotFound, kind, id) }
