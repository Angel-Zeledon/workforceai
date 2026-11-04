package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/orgchart"
)

// Folder access per agent (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 3):
// "Carpetas que puede ver" in the agent panel and "Qué agentes la ven" in Files. Reading needs files:read; changing
// needs files:grant (admins and the owner) OR being the head of the agent's team (its department in the org chart).
// Every change is audited by the drive (drive.access.changed).

func (s *server) mountFolderAccess(r chi.Router) {
	if s.Drive == nil {
		return
	}
	read := s.can(auth.PermFilesRead)
	r.With(read).Get("/agents/{id}/folder-access", s.agentFolderAccess)
	r.With(read).Put("/agents/{id}/folder-access", s.putAgentFolderAccess)
	r.With(read).Get("/drive/folder-access", s.driveFolderAccess)
}

func (s *server) agentFolders() *drive.AgentFS {
	if s.AgentFolders != nil {
		return s.AgentFolders
	}
	return &drive.AgentFS{Svc: s.Drive} // no project directory: defaults without project folders
}

// canManageAgentAccess: admins/owner (files:grant), or the person who heads the agent's department.
func (s *server) canManageAgentAccess(r *http.Request, agentID string) bool {
	if !s.AuthEnabled {
		return true
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		return false
	}
	if p.Can(auth.PermFilesGrant) {
		return true
	}
	return s.headsTeamOf(r, p.UserID, agentID)
}

// headsTeamOf reports whether userID heads the department (or a parent department) the agent belongs to.
func (s *server) headsTeamOf(r *http.Request, userID, agentID string) bool {
	if s.OrgChart == nil || userID == "" {
		return false
	}
	job, err := s.OrgChart.Job(r.Context(), agentID)
	if err != nil || job.DepartmentID == "" {
		return false
	}
	deps, err := s.OrgChart.Departments(r.Context())
	if err != nil {
		return false
	}
	byID := map[string]orgchart.Department{}
	for _, d := range deps {
		byID[d.ID] = d
	}
	for id, n := job.DepartmentID, 0; id != "" && n < 32; n++ {
		d, ok := byID[id]
		if !ok {
			return false
		}
		if d.Head.Kind == orgchart.KindPerson && d.Head.ID == userID {
			return true
		}
		id = d.ParentID
	}
	return false
}

// agentFolderAccess: GET /agents/{id}/folder-access -> {entries, can_manage}.
func (s *server) agentFolderAccess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Queries.Agent(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	es, err := s.agentFolders().AgentAccess(r.Context(), id)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list(es), "can_manage": s.canManageAgentAccess(r, id)})
}

// putAgentFolderAccess: PUT /agents/{id}/folder-access {path, level: none|read|edit|default}.
func (s *server) putAgentFolderAccess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in struct {
		Path  string `json:"path"`
		Level string `json:"level"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Queries.Agent(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	if !s.canManageAgentAccess(r, id) {
		codeErr(w, http.StatusForbidden, "forbidden", "only admins and the head of the agent's office change its folders")
		return
	}
	prev, err := s.Drive.SetAgentAccess(r.Context(), s.drivePrincipal(r), id, in.Path, strings.ToLower(strings.TrimSpace(in.Level)))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	es, err := s.agentFolders().AgentAccess(r.Context(), id)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"previous": prev, "entries": list(es), "can_manage": true})
}

type folderAgent struct {
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	CanManage bool   `json:"can_manage"`
}

// driveFolderAccess: GET /drive/folder-access?path= -> {path, agents: [{agent_id, name, level, source, can_manage}]}.
func (s *server) driveFolderAccess(w http.ResponseWriter, r *http.Request) {
	p, err := drive.CleanPath(r.URL.Query().Get("path"))
	if err != nil || p == "/" {
		codeErr(w, http.StatusBadRequest, "path_invalid", "a folder path below / is required")
		return
	}
	agents, err := s.Queries.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	fa := s.agentFolders()
	out := make([]folderAgent, 0, len(agents))
	for _, a := range agents {
		lv, src, err := fa.AccessAt(r.Context(), a.ID, p)
		if err != nil {
			s.driveFail(w, err)
			return
		}
		out = append(out, folderAgent{AgentID: a.ID, Name: a.Name, Level: lv, Source: src, CanManage: s.canManageAgentAccess(r, a.ID)})
	}
	rank := map[string]int{drive.AccessEdit: 0, drive.AccessRead: 1, drive.AccessNone: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Level] != rank[out[j].Level] {
			return rank[out[i].Level] < rank[out[j].Level]
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "agents": out})
}
