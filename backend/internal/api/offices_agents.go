package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// Agent owners and visibility (E2, equipos-oficinas.md §4): GET /agents/directory, GET and PUT /agents/{id}/access,
// and the filter of GET /agents, GET /agents/{id}[/detail] and the direct chat with an agent.
func (s *server) mountAgentAccess(r chi.Router) {
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/agents/directory", s.agentDirectory)
	r.With(read).Get("/agents/{id}/access", s.getAgentAccess)
	r.With(read).Put("/agents/{id}/access", s.putAgentAccess)
}

type shareView struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Level  string `json:"level"`
}

// accessView is the access of one agent as a viewer sees it.
type accessView struct {
	OwnerKind         string         `json:"owner_kind"`
	OwnerID           string         `json:"owner_id"`
	OwnerName         string         `json:"owner_name"`
	OfficeID          string         `json:"office_id"`
	OfficeName        string         `json:"office_name"`
	Visibility        string         `json:"visibility"`
	Shares            []shareView    `json:"shares"` // only for whoever may edit the agent
	Explicit          bool           `json:"explicit"`
	Departed          bool           `json:"departed,omitempty"`
	PreviousOwnerName string         `json:"previous_owner_name,omitempty"`
	Rights            offices.Rights `json:"rights"`
}

func (s *server) accessOf(m *offices.Model, v offices.Viewer, agentID string) accessView {
	ea, rights := m.Agent(agentID), m.Rights(v, agentID)
	av := accessView{OwnerKind: ea.OwnerKind, OwnerID: ea.OwnerID, OfficeID: ea.OfficeID, Visibility: ea.Visibility,
		Shares: []shareView{}, Explicit: ea.Explicit, Departed: ea.Departed, Rights: rights}
	if o, ok := m.Office(ea.OfficeID); ok {
		av.OfficeName = o.Name
	}
	if ea.OwnerKind == offices.OwnerPerson {
		p, _ := m.Person(ea.OwnerID)
		av.OwnerName = p.Name
	} else {
		av.OwnerName = av.OfficeName
	}
	if ea.Departed {
		av.PreviousOwnerName = ea.PreviousOwner // they left: only the id is known
	}
	if rights.Edit {
		for _, sh := range ea.Shares {
			p, _ := m.Person(sh.UserID)
			av.Shares = append(av.Shares, shareView{UserID: sh.UserID, Name: p.Name, Level: sh.Level})
		}
	}
	return av
}

// agentModel returns the offices model when it can change what this request sees (auth on, a person, rules);
// nil = everybody sees every agent, as before.
func (s *server) agentModel(r *http.Request) (*offices.Model, offices.Viewer, error) {
	if s.Offices == nil || !s.AuthEnabled {
		return nil, offices.Viewer{}, nil
	}
	v := s.viewer(r)
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		return nil, v, err
	}
	if !m.Restricted() {
		return nil, v, nil
	}
	return m, v, nil
}

// agentView is an agent plus its access (GET /agents when the organization has offices or rules).
type agentView struct {
	domain.Agent
	Access *accessView `json:"access,omitempty"`
}

// visibleAgents filters GET /agents: the agents the viewer sees, each with its access.
func (s *server) visibleAgents(r *http.Request, agents []domain.Agent) (any, error) {
	m, v, err := s.agentModel(r)
	if err != nil || m == nil {
		return list(agents), err
	}
	out := make([]agentView, 0, len(agents))
	for _, a := range agents {
		av := s.accessOf(m, v, a.ID)
		if !av.Rights.See {
			continue
		}
		if !m.CanSeeWork(v, a.ID) {
			a = hideActivity(a)
		}
		out = append(out, agentView{Agent: a, Access: &av})
	}
	return out, nil
}

// hideActivity blanks what an agent's live state says about its work (D3, final review I2): the activity line names
// the current or last task ("Completó: <title>"), with its id and progress. The state stays: the agent is listed.
func hideActivity(a domain.Agent) domain.Agent {
	a.Activity, a.CurrentTaskID, a.Progress = "", nil, 0
	return a
}

// agentHidden reports whether the viewer may not see the agent (GET /agents/{id} answers 404 then).
func (s *server) agentHidden(r *http.Request, agentID string) (bool, error) {
	m, v, err := s.agentModel(r)
	if err != nil || m == nil {
		return false, err
	}
	return !m.Rights(v, agentID).See, nil
}

// errAgentNotUsable is the answer to a direct chat with an agent the person may not use.
var errAgentNotUsable = fmt.Errorf("%w: you cannot use this agent; ask its owner or its office to share it", domain.ErrForbidden)

// checkAgentUse refuses a direct chat (conversation agent:<id>) with an agent the person may not use (D3: an admin
// outside a private office does not use its agents unless they are shared with them or in the directory).
func (s *server) checkAgentUse(r *http.Request, conversation string) error {
	id, ok := strings.CutPrefix(domain.PublicChatConv(conversation), domain.ChatAgentPrefix)
	if !ok || id == "" {
		return nil
	}
	m, v, err := s.agentModel(r)
	if err != nil || m == nil {
		return err
	}
	if !m.Rights(v, id).Use {
		return errAgentNotUsable
	}
	return nil
}

type directoryEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Title string `json:"title"`
	accessView
}

func (s *server) agentDirectory(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []directoryEntry{}
	for _, a := range m.Agents() {
		av := s.accessOf(m, v, a.ID)
		if !av.Rights.See {
			continue
		}
		out = append(out, directoryEntry{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title, accessView: av})
	}
	writeJSON(w, 200, map[string]any{"agents": out, "restricted": m.Restricted()})
}

func (s *server) getAgentAccess(w http.ResponseWriter, r *http.Request) {
	v, id := s.viewer(r), chi.URLParam(r, "id")
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !knownAgent(m, id) || !m.Rights(v, id).See {
		s.fail(w, fmt.Errorf("%w: agent %s", domain.ErrNotFound, id))
		return
	}
	writeJSON(w, 200, s.accessOf(m, v, id))
}

func knownAgent(m *offices.Model, id string) bool {
	for _, a := range m.Agents() {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (s *server) putAgentAccess(w http.ResponseWriter, r *http.Request) {
	var in offices.AccessInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, id := s.viewer(r), chi.URLParam(r, "id")
	if m, err := s.Offices.Model(r.Context(), s.org(r)); err == nil && knownAgent(m, id) && !m.Rights(v, id).See {
		s.fail(w, fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)) // do not reveal an agent the viewer cannot see
		return
	}
	if _, err := s.Offices.SetAgentAccess(r.Context(), s.org(r), v, id, in); err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.accessOf(m, v, id))
}

// hideAgent answers 404 for an agent the viewer may not see and reports whether it did.
func (s *server) hideAgent(w http.ResponseWriter, r *http.Request) bool {
	id := chi.URLParam(r, "id")
	hidden, err := s.agentHidden(r, id)
	if err != nil {
		s.fail(w, err)
		return true
	}
	if hidden {
		s.fail(w, fmt.Errorf("%w: agent %s", domain.ErrNotFound, id))
	}
	return hidden
}
