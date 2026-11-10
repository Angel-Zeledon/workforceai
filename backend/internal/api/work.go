package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// The work of a private department (D3, equipos-oficinas.md §4.1): the requests, tasks, reports, activity, chats and
// memory of an agent of a private office are only its members', admins included. Whoever may use such an agent
// (shared with them, in the directory) talks with it in their own thread and sees the requests they asked for, not
// the rest of the office's work. What is hidden answers exactly like what does not exist: left out of the listings,
// 404 in a detail.

// workScope is the rule for one HTTP request: the offices model is loaded once (lazily) and the visibility of each
// request is computed once.
type workScope struct {
	s      *server
	ctx    context.Context
	org    string
	loaded bool
	m      *offices.Model // nil: everything visible (no offices, no auth, nothing restricted)
	v      offices.Viewer
	err    error
	agents map[string][]string // request -> the agents of its tasks (filled by index or on demand)
	seen   map[string]bool     // request -> visible
	r      *http.Request
}

func (s *server) work(r *http.Request) *workScope {
	return &workScope{s: s, r: r, ctx: r.Context(), org: s.org(r), agents: map[string][]string{}, seen: map[string]bool{}}
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

// use reports whether the person may use an agent (E2 rights).
func (w *workScope) use(id string) bool {
	if err := w.load(); err != nil {
		return false
	}
	return w.m == nil || w.m.Rights(w.v, id).Use
}

// chat reports whether the person may open their own chat with an agent: they see its work or may use it.
func (w *workScope) chat(id string) bool { return w.agent(id) || w.use(id) }

// ownChat reports whether the person already has their own thread with an agent: it stays readable to them even
// when they can no longer use the agent (read only; sending is checkAgentUse's).
func (w *workScope) ownChat(id string) bool {
	return application.HasOwnChat(w.ctx, w.s.Store, w.org, id)
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
	tasks, err := w.s.Store.ListTasks(w.ctx, w.org, "", "")
	if err != nil {
		return err
	}
	w.index(tasks)
	return nil
}

// request reports whether the person sees a request: they asked for it (always), or they see the work of every
// agent of its tasks. A request without tasks yet (planning) is its requester's and the admins'.
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
		tasks, err := w.s.Store.ListTasksByRequest(w.ctx, w.org, id)
		if err != nil {
			return false
		}
		w.index(tasks)
		agents = w.agents[id]
	}
	vis := len(agents) > 0 || w.v.Admin
	for _, a := range agents {
		if !w.m.CanSeeWork(w.v, a) {
			vis = false
			break
		}
	}
	if !vis && w.v.UserID != "" && w.s.Orch != nil && w.s.Orch.RequestedBy(w.ctx, id) == w.v.UserID {
		vis = true
	}
	w.seen[id] = vis
	return vis
}

// conversation reports whether the person sees a stored conversation: a request's conversation is its request's;
// another one needs the work of every participant.
func (w *workScope) conversation(c domain.Conversation) bool {
	if c.RequestID != nil && *c.RequestID != "" {
		return w.request(*c.RequestID)
	}
	for _, p := range c.Participants {
		if !w.agent(p) {
			return false
		}
	}
	return true
}

// conversationID reports whether the person sees a conversation by id: a chat with an agent (their own thread)
// when they may chat with it (or, with own, when that thread already exists: reading it and its read marker), else
// the stored conversation. A missing one is left to the caller's own not found; any other error fails closed.
func (w *workScope) conversationID(id string, own bool) (bool, error) {
	if application.IsChatConversation(id) {
		agent, ok := strings.CutPrefix(domain.PublicChatConv(id), domain.ChatAgentPrefix)
		vis := !ok || w.chat(agent)
		if !vis && w.err == nil && own {
			vis = w.ownChat(agent)
		}
		return vis, w.err
	}
	if err := w.load(); err != nil || w.m == nil {
		return err == nil, err
	}
	c, err := w.s.Store.GetConversation(w.ctx, w.org, id)
	if errors.Is(err, domain.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return w.conversation(c), nil
}

// filter is the rule for the application's reads (the chat history, the office chat's routing).
func (w *workScope) filter() application.WorkFilter {
	return application.WorkFilter{Agent: w.agent, Use: w.use, Request: w.request}
}

// context returns the request carrying the rule in its context.
func (w *workScope) context() *http.Request {
	return w.r.WithContext(application.WithWorkFilter(w.r.Context(), w.filter()))
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
		s.fail(w, notFound("agent", id))
		return true
	}
	return false
}

// notFound is the answer to something hidden: the same as something missing.
func notFound(kind, id string) error { return fmt.Errorf("%w: %s %s", domain.ErrNotFound, kind, id) }

// workAgent guards the routes about the work of the agent {id}.
func (s *server) workAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hideWork(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// workRequest guards the routes about the request {id}.
func (s *server) workRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, id := s.work(r), chi.URLParam(r, "id")
		if !ws.request(id) {
			s.fail(w, cmp.Or(ws.err, notFound("request", id)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// workTask guards the routes about the task {id} (a missing task is left to the handler).
func (s *server) workTask(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, id := s.work(r), chi.URLParam(r, "id")
		if err := ws.load(); err != nil {
			s.fail(w, err)
			return
		}
		if !ws.open() {
			t, err := s.Store.GetTask(r.Context(), ws.org, id)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				s.fail(w, err)
				return
			}
			if err == nil && !ws.agent(t.AgentID) {
				s.fail(w, notFound("task", id))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// workConversation guards the writes on the conversation {id} (rename, delete): one's own thread with an agent one
// can no longer use is read only.
func (s *server) workConversation(next http.Handler) http.Handler {
	return s.guardConversation(next, false)
}

// readOwnConversation guards marking the conversation {id} read: allowed on one's own read-only thread.
func (s *server) readOwnConversation(next http.Handler) http.Handler {
	return s.guardConversation(next, true)
}

func (s *server) guardConversation(next http.Handler, own bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := s.work(r).conversationID(id, own); err != nil || !ok {
			s.fail(w, cmp.Or(err, notFound("conversation", id)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkMentions refuses an office chat message that calls by @name an agent the person may not use (the same
// answer as a direct chat with it, checkAgentUse).
func (s *server) checkMentions(r *http.Request, text string) error {
	if !strings.Contains(text, "@") {
		return nil
	}
	ws := s.work(r)
	if err := ws.load(); err != nil || ws.m == nil {
		return err
	}
	agents, err := s.Store.ListAgents(r.Context(), ws.org)
	if err != nil {
		return err
	}
	for _, word := range strings.Fields(text) {
		ref, ok := strings.CutPrefix(word, "@")
		if !ok {
			continue
		}
		ref = commands.Fold(strings.TrimRight(ref, ".,;:!?¿¡)\"'"))
		if ref == "" {
			continue
		}
		for _, a := range agents {
			first, _, _ := strings.Cut(commands.Fold(a.Name), " ")
			if (commands.Fold(a.ID) == ref || first == ref) && !ws.use(a.ID) {
				return errAgentNotUsable
			}
		}
	}
	return nil
}
