package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/roles"
)

// E2 "Oficinas" (docs/architecture/equipos-oficinas.md §3 and §4; docs/superpowers/plans/2026-11-03-e2-oficinas.md):
// offices with members, a responsible and open/private; who owns each agent and who may see or use it; the agent
// directory; office and personal memory. Everybody with agents:read reads; the offices service decides every change
// (an admin, the office's responsible, the agent's owner). Optional: no-op when Deps.Offices is nil.
func (s *server) mountOffices(r chi.Router) {
	if s.Offices == nil {
		return
	}
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/offices", s.listOffices)
	r.With(read).Post("/offices", s.enableOffice)
	r.With(read).Put("/offices/{id}", s.updateOffice)
	r.With(read).Delete("/offices/{id}", s.disableOffice)
	r.With(read).Put("/offices/{id}/members/{user_id}", s.putOfficeMember)
	r.With(read).Delete("/offices/{id}/members/{user_id}", s.deleteOfficeMember)
	s.mountAgentAccess(r)
	s.mountTeamNotes(r)
}

// viewer is the person behind the request. Without authentication (the open demo) the caller is an admin with no
// person, and nothing is filtered.
func (s *server) viewer(r *http.Request) offices.Viewer {
	if !s.AuthEnabled {
		return offices.Viewer{Admin: true}
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		return offices.Viewer{}
	}
	return offices.Viewer{UserID: p.UserID, Admin: p.Role == auth.RoleOwner || p.Role == auth.RoleAdmin}
}

type personRef struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type officeView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Visibility  string      `json:"visibility"`
	General     bool        `json:"general"`
	Leads       []personRef `json:"leads"`
	Members     []personRef `json:"members"` // empty when the viewer cannot look inside
	MemberCount int         `json:"member_count"`
	Agents      []string    `json:"agents"` // the agents of the office the viewer sees
	FolderPath  string      `json:"folder_path"` // "" when the office has no drive folder
	CanEnter    bool        `json:"can_enter"`
	CanManage   bool        `json:"can_manage"`
	IsMember    bool        `json:"is_member"`
	IsLead      bool        `json:"is_lead"`
}

func (s *server) people(m *offices.Model, ids []string) []personRef {
	out := make([]personRef, 0, len(ids))
	for _, id := range ids {
		p, _ := m.Person(id)
		out = append(out, personRef{UserID: id, Name: p.Name})
	}
	return out
}

// officeFolderPath is the path of the office's drive folder, "" when there is none (or no drive).
func (s *server) officeFolderPath(ctx context.Context, officeID string) string {
	if s.Drive == nil {
		return ""
	}
	f, err := s.Drive.OfficeFolder(ctx, officeID)
	if err != nil {
		return ""
	}
	return f.Path
}

func (s *server) officeView(ctx context.Context, m *offices.Model, v offices.Viewer, o offices.Office) officeView {
	ov := officeView{ID: o.ID, FolderPath: s.officeFolderPath(ctx, o.ID), Name: o.Name, Visibility: o.Visibility, General: o.General, Leads: s.people(m, o.Leads),
		Members: []personRef{}, MemberCount: len(o.Members), Agents: []string{}, CanEnter: m.CanEnter(v, o.ID),
		CanManage: m.CanManage(v, o.ID), IsMember: m.IsMember(v.UserID, o.ID), IsLead: m.IsLead(v.UserID, o.ID)}
	if ov.CanEnter {
		ov.Members = s.people(m, o.Members)
		for _, a := range m.Agents() {
			if m.Agent(a.ID).OfficeID == o.ID && m.Rights(v, a.ID).See {
				ov.Agents = append(ov.Agents, a.ID)
			}
		}
	}
	return ov
}

func (s *server) listOffices(w http.ResponseWriter, r *http.Request) {
	v := s.viewer(r)
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]officeView, 0, len(m.Offices()))
	isOffice := map[string]bool{}
	for _, o := range m.Offices() {
		isOffice[o.ID] = true
		out = append(out, s.officeView(r.Context(), m, v, o))
	}
	type dept struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Office bool   `json:"office"`
	}
	depts := []dept{}
	for _, d := range m.Departments() {
		depts = append(depts, dept{ID: d.ID, Name: d.Name, Office: isOffice[d.ID]})
	}
	writeJSON(w, 200, map[string]any{"offices": out, "departments": depts, "restricted": m.Restricted(),
		"me": map[string]any{"user_id": v.UserID, "admin": v.Admin, "offices": list(m.OfficesOf(v.UserID))}})
}

func (s *server) writeOffice(w http.ResponseWriter, r *http.Request, o offices.Office, status int) {
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, status, s.officeView(r.Context(), m, s.viewer(r), o))
}

func (s *server) enableOffice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DepartmentID string `json:"department_id"`
		Visibility   string `json:"visibility"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.Offices.Enable(r.Context(), s.org(r), s.viewer(r), body.DepartmentID, body.Visibility)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.ensureOfficeFolder(r.Context(), o, roles.ProfessionOther)
	s.writeOffice(w, r, o, http.StatusCreated)
}

// ensureOfficeFolder gives the office its drive folder with the profession's template. Failing does not undo the
// office: it is logged and the office simply has no folder yet.
func (s *server) ensureOfficeFolder(ctx context.Context, o offices.Office, profession string) {
	if s.Drive == nil {
		return
	}
	prof, _ := roles.ProfessionOf(profession)
	loc := s.teamLocale(ctx, application.OrgFrom(ctx, s.Cfg.OrgID))
	if _, err := s.Drive.EnsureOfficeFolder(ctx, o.ID, o.Name, prof.Folders(loc)); err != nil {
		s.Log.Warn("offices: the office folder was not created", "office", o.ID, "err", err)
	}
}

func (s *server) updateOffice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Visibility string `json:"visibility"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.Offices.SetVisibility(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), body.Visibility)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeOffice(w, r, o, 200)
}

func (s *server) disableOffice(w http.ResponseWriter, r *http.Request) {
	if err := s.Offices.Disable(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) putOfficeMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.Offices.AddMember(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), chi.URLParam(r, "user_id"), body.Role)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeOffice(w, r, o, 200)
}

func (s *server) deleteOfficeMember(w http.ResponseWriter, r *http.Request) {
	o, err := s.Offices.RemoveMember(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), chi.URLParam(r, "user_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeOffice(w, r, o, 200)
}

// officeLayoutAllowed guards the layout of one office (E2): reading needs to be able to look inside it (an open
// office or one of yours), saving needs to be its responsible or an admin. General (or no office) is as before.
func (s *server) officeLayoutAllowed(r *http.Request, office string, save bool) error {
	if office == "" || office == offices.GeneralID {
		return nil
	}
	if s.Offices == nil {
		return fmt.Errorf("%w: this server has one office layout per organization", domain.ErrInvalid)
	}
	m, err := s.Offices.Model(r.Context(), s.org(r))
	if err != nil {
		return err
	}
	if _, ok := m.Office(office); !ok {
		return fmt.Errorf("%w: office %s", domain.ErrNotFound, office)
	}
	v := s.viewer(r)
	if !m.CanEnter(v, office) {
		return fmt.Errorf("%w: office %s", domain.ErrNotFound, office) // a private office you are not in
	}
	if save && !m.CanManage(v, office) {
		return fmt.Errorf("%w: only its responsible or an admin changes an office's layout", domain.ErrForbidden)
	}
	return nil
}
