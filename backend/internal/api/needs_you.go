package api

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/needsyou"
	"aiworkforce/backend/internal/offices"
)

// "Te necesitan" (docs/architecture/needs-you.md): one list of what waits for the person who looks and that they may
// resolve. Read-only: every action goes through the route of its own screen (deciding an approval, answering a
// question, accepting a suggestion, deciding a trust proposal), with all of its rules.
//
//	GET /needs-you?limit=   the items, most urgent first, then the oldest (at most limit, 1..500, default 200)
//	GET /needs-you/count    the same counts without the items (the badge of the menu)
//
// Each source applies the rule its own screen applies, never a new one: what a person does not see or may not resolve
// is not listed and not counted. A principal that is not a person (an agent, a token, the system) gets nothing. To add a
// source, write one more needsyou.Source below (a new kind of initiative raised by any signal already arrives as a
// suggestion).

func (s *server) mountNeedsYou(r chi.Router) {
	read := s.can(auth.PermActivityRead) // every role holds it: each source checks its own permission
	r.With(read).Get("/needs-you", s.needsYou)
	r.With(read).Get("/needs-you/count", s.needsYouCount)
}

const (
	needsYouLimitDefault = 200
	needsYouLimitMax     = 500
)

func (s *server) needsYou(w http.ResponseWriter, r *http.Request) {
	limit := needsYouLimitDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			codeErr(w, http.StatusBadRequest, "invalid", "limit must be a positive number")
			return
		}
		limit = min(n, needsYouLimitMax)
	}
	r = r.WithContext(offices.Once(r.Context())) // the office rule is read once for the whole list
	res := s.collectNeedsYou(r, needsyou.Options{Full: true, Limit: limit})
	s.decorateNeedsYou(r, res.Items)
	writeJSON(w, http.StatusOK, res)
}

func (s *server) needsYouCount(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(offices.Once(r.Context()))
	writeJSON(w, http.StatusOK, s.collectNeedsYou(r, needsyou.Options{CountOnly: true}))
}

// nyViewer is the person of the request as the sources see them. Without authentication the demo acts as its owner
// (like the approvals inbox).
type nyViewer struct {
	person bool
	uid    string
	role   string
	can    func(auth.Permission) bool
	ws     *workScope
}

func (s *server) needsYouViewer(r *http.Request) nyViewer {
	v := nyViewer{ws: s.work(r)}
	if !s.AuthEnabled {
		v.person, v.uid, v.role, v.can = true, "demo", string(auth.RoleOwner), func(auth.Permission) bool { return true }
		return v
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || application.PersonID(p.UserID) == "" {
		return v // agents, tokens and the system resolve nothing here
	}
	v.person, v.uid, v.role, v.can = true, p.UserID, string(p.Role), p.Can
	return v
}

func (s *server) collectNeedsYou(r *http.Request, o needsyou.Options) needsyou.Result {
	v := s.needsYouViewer(r)
	if !v.person {
		return needsyou.Collect(r.Context(), o)
	}
	res := needsyou.Collect(r.Context(), o,
		s.nyApprovals(r, v), s.nyQuestions(r, v), s.nySuggestions(r, v), s.nyTrust(r, v), s.nyHelpers(r, v))
	if len(res.Unavailable) > 0 && s.Log != nil {
		s.Log.Warn("needs-you: a source could not be read", "sources", res.Unavailable)
	}
	return res
}

// nyApprovals: the pending approvals the person sees and may decide now (GET /approvals/inbox?mine=decidable: the
// office rule, approvals:decide or being the office's responsible, minimum role, no self-approval, a different person
// for each approval of a double approval).
func (s *server) nyApprovals(r *http.Request, v nyViewer) needsyou.Source {
	if s.Approvals == nil {
		return needsyou.Source{}
	}
	return needsyou.Source{Kind: needsyou.KindApproval, Collect: func(ctx context.Context, full bool) ([]needsyou.Item, error) {
		if err := v.ws.load(); err != nil {
			return nil, err
		}
		org := s.org(r)
		pending, err := s.Store.ListApprovals(ctx, org, string(domain.ApprovalPending))
		if err != nil {
			return nil, err
		}
		pol := s.Approvals.Policy()
		if !s.AuthEnabled {
			pol = nil // the demo acts as its owner (getApprovalInbox)
		}
		hasDecide := v.can(auth.PermApprovalsDecide)
		var names func(string) string
		if full {
			names = s.projectNamer(r)
		}
		tasks := map[string]string{}
		var out []needsyou.Item
		for _, ap := range pending {
			if !v.ws.seesApproval(ap) || !application.DecidesApproval(ctx, pol, org, ap, v.uid, v.role, hasDecide) {
				continue
			}
			it := needsyou.Item{ID: ap.ID, Title: cmp.Or(ap.Title, ap.Action), AgentID: ap.AgentID, CreatedAt: ap.CreatedAt,
				Risk: ap.Risk, Double: ap.RequiredApprovals >= 2, Action: ap.Action, Target: "/approvals/" + url.PathEscape(ap.ID),
				Direct: !application.IsHighRiskApproval(ap)}
			if full {
				if title, seen := tasks[ap.TaskID]; seen {
					it.Detail = title
				} else if ap.TaskID != "" {
					if t, err := s.Store.GetTask(ctx, org, ap.TaskID); err == nil {
						it.Detail, tasks[ap.TaskID] = t.Title, t.Title
					}
				}
				if ap.ProjectID != "" && names != nil {
					it.ProjectName = names(ap.ProjectID)
				}
			}
			out = append(out, it)
		}
		return out, nil
	}}
}

// nyQuestions: the open questions of the agents waiting for this person: the ones for them (they launched the work)
// and, for who may answer any (tasks:manage), the ones nobody launched (they go to the owners and admins). A question
// for somebody else is theirs, even when an admin could answer it. D3: seesQuestion, as GET /questions.
func (s *server) nyQuestions(r *http.Request, v nyViewer) needsyou.Source {
	if s.Orch == nil || s.Orch.Questions() == nil || !v.can(auth.PermTasksCreate) {
		return needsyou.Source{}
	}
	return needsyou.Source{Kind: needsyou.KindQuestion, Collect: func(ctx context.Context, full bool) ([]needsyou.Item, error) {
		if err := v.ws.load(); err != nil {
			return nil, err
		}
		org := s.org(r)
		qs, err := s.Orch.Questions().Store.ListQuestions(ctx, org, application.QuestionOpen)
		if err != nil {
			return nil, err
		}
		uid, manage := s.answerer(r)
		var names func(string) string
		if full {
			names = s.projectNamer(r)
		}
		var out []needsyou.Item
		for _, q := range qs {
			if !v.ws.open() && !v.ws.seesQuestion(q) {
				continue
			}
			forMe := q.AskedFor != "" && q.AskedFor == uid
			if !forMe && !(q.AskedFor == "" && manage) && s.AuthEnabled {
				continue
			}
			it := needsyou.Item{ID: q.ID, Title: cmp.Or(q.Need, q.WhatHappened), AgentID: q.AgentID, CreatedAt: q.CreatedAt,
				Target: "/approvals/inbox", Options: q.Options}
			if full {
				if t, err := s.Store.GetTask(ctx, org, q.TaskID); err == nil {
					it.Detail = t.Title
				}
				if q.ProjectID != "" && names != nil {
					it.ProjectName = names(q.ProjectID)
				}
			}
			out = append(out, it)
		}
		return out, nil
	}}
}

// nySuggestions: the pending suggestions of the agents whose work the person sees, for who may accept them
// (requests:create, as POST /initiatives/{id}/accept). Accepting one starts a normal request.
func (s *server) nySuggestions(r *http.Request, v nyViewer) needsyou.Source {
	if s.Initiative == nil || !v.can(auth.PermRequestsCreate) {
		return needsyou.Source{}
	}
	return needsyou.Source{Kind: needsyou.KindSuggestion, Collect: func(ctx context.Context, _ bool) ([]needsyou.Item, error) {
		if err := v.ws.load(); err != nil {
			return nil, err
		}
		list, err := s.Initiative.List(ctx, application.InitiativePending, initiativeListWindow)
		if err != nil {
			return nil, err
		}
		var out []needsyou.Item
		for _, it := range list {
			if !v.ws.agentOK(it.AgentID) {
				continue
			}
			out = append(out, needsyou.Item{ID: it.ID, Title: it.Title, Detail: it.Body, AgentID: it.AgentID, CreatedAt: it.CreatedAt,
				Signal: it.Signal, Params: it.Params, Target: "/dashboard", Direct: true})
		}
		return out, nil
	}}
}

// nyTrust: the pending trust proposals the person may decide (agents:write and approvals:decide, as the routes, and the
// service's rule: admin or owner, never an agent) of agents whose work they see. Deciding one changes how much an agent
// may do alone: the list only links to the agent's trust card.
func (s *server) nyTrust(r *http.Request, v nyViewer) needsyou.Source {
	if s.Trust == nil || !v.can(auth.PermAgentsWrite) || !v.can(auth.PermApprovalsDecide) {
		return needsyou.Source{}
	}
	return needsyou.Source{Kind: needsyou.KindTrust, Collect: func(ctx context.Context, _ bool) ([]needsyou.Item, error) {
		if err := v.ws.load(); err != nil {
			return nil, err
		}
		ps, err := s.Trust.Pending(ctx, needsYouLimitMax)
		if err != nil {
			return nil, err
		}
		var out []needsyou.Item
		for _, p := range ps {
			if !v.ws.agent(p.AgentID) || !s.Trust.MayDecide(ctx, p.AgentID) {
				continue
			}
			out = append(out, needsyou.Item{ID: p.ID, AgentID: p.AgentID, CreatedAt: p.CreatedAt, ProposalKind: p.Kind,
				FromLevel: p.FromLevel, ToLevel: p.ToLevel, Target: "/?agent=" + url.QueryEscape(p.AgentID)})
		}
		return out, nil
	}}
}

// nyHelpers: the helper proposals waiting for the person who asked for the request (and may still ask its responsible
// for work, as accepting does). Without authentication the demo acts as the owner of everything. The request waits
// until they answer, so it is blocked work like an approval.
func (s *server) nyHelpers(r *http.Request, v nyViewer) needsyou.Source {
	if s.Orch == nil || !s.Orch.HelpersEnabled() || !v.can(auth.PermConversationsPost) {
		return needsyou.Source{}
	}
	return needsyou.Source{Kind: needsyou.KindHelpers, Collect: func(ctx context.Context, full bool) ([]needsyou.Item, error) {
		if err := v.ws.load(); err != nil {
			return nil, err
		}
		hs, ok := s.Store.(application.HelperStore)
		if !ok {
			return nil, nil
		}
		gs, err := hs.ListHelperGroups(ctx, s.org(r), domain.HelperProposed)
		if err != nil {
			return nil, err
		}
		var out []needsyou.Item
		for _, g := range gs {
			if s.AuthEnabled && s.Orch.RequestedBy(ctx, g.RequestID) != v.uid { // it is the asker's to answer
				continue
			}
			if !v.ws.request(g.RequestID) || !v.ws.use(g.OwnerAgentID) {
				continue
			}
			it := needsyou.Item{ID: g.RequestID, Title: fmt.Sprintf("Repartir en %d ayudantes", g.Count), AgentID: g.OwnerAgentID, CreatedAt: g.CreatedAt,
				Target: "/requests/" + url.PathEscape(g.RequestID)}
			if full {
				if req, err := s.Store.GetRequest(ctx, s.org(r), g.RequestID); err == nil {
					it.Detail = req.Text
					if len([]rune(it.Detail)) > 140 {
						it.Detail = string([]rune(it.Detail)[:140]) + "…"
					}
				}
			}
			out = append(out, it)
		}
		return out, nil
	}}
}

// decorateNeedsYou adds the names of the agents and of their departments (General is left out) to the listed items.
func (s *server) decorateNeedsYou(r *http.Request, items []needsyou.Item) {
	if len(items) == 0 {
		return
	}
	ctx, org := r.Context(), s.org(r)
	names := map[string]string{}
	if agents, err := s.Store.ListAgents(ctx, org); err == nil {
		for _, a := range agents {
			names[a.ID] = a.Name
		}
	}
	var m *offices.Model
	if s.Offices != nil {
		m, _ = s.Offices.Model(ctx, org)
	}
	for i := range items {
		it := &items[i]
		if it.AgentID == "" {
			continue
		}
		it.AgentName = cmp.Or(names[it.AgentID], it.AgentID)
		if m == nil {
			continue
		}
		if o, ok := m.Office(m.Agent(it.AgentID).OfficeID); ok && !o.General {
			it.OfficeID, it.OfficeName = o.ID, o.Name
		}
	}
}
