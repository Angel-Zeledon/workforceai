package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
	ot "aiworkforce/backend/internal/officetemplates"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/skills"
)

// Office templates (F3, docs/architecture/office-templates.md): save a department (or the company) as a template
// file and take it to another organization, with the same preview and install as a ready-to-hire team (F2).
//
//	GET    /office-templates/export?office_id=<id>|scope=company   the file, built from an allow-list
//	POST   /office-templates/import                                upload a file (validated strictly, kept once)
//	GET    /office-templates                                       "Your templates" in the Teams gallery
//	GET    /office-templates/{id}?office_id=                       preview for this organization
//	POST   /office-templates/{id}/install {office_id?}             install (idempotent)
//	DELETE /office-templates/{id}
func (s *server) mountOfficeTemplates(r chi.Router) {
	if s.OfficeTemplates == nil {
		return
	}
	r.With(s.can(auth.PermAgentsRead)).Get("/office-templates/export", s.exportOfficeTemplate)
	r.With(s.can(auth.PermAgentsWrite)).Post("/office-templates/import", s.importOfficeTemplate)
	r.With(s.can(auth.PermAgentsRead)).Get("/office-templates", s.listOfficeTemplates)
	r.With(s.can(auth.PermAgentsRead)).Get("/office-templates/{id}", s.getOfficeTemplate)
	r.With(s.can(auth.PermAgentsWrite)).Post("/office-templates/{id}/install", s.installOfficeTemplate)
	r.With(s.can(auth.PermAgentsWrite)).Delete("/office-templates/{id}", s.deleteOfficeTemplate)
}

// ---------------------------------------------------------------- export

type otExportView struct {
	FileName string      `json:"file_name"`
	Template ot.Template `json:"template"`
	// LeftOut is what the allow-list or the filters kept out of this file, in plain words.
	LeftOut []string `json:"left_out"`
}

// exportScope is what an export covers: the offices (General and departments) and, for each, the key in the file.
type exportScope struct {
	kind    string
	offices []offices.Office
	key     map[string]string // office id -> department key
}

func (s *server) exportScopeOf(r *http.Request, m *offices.Model) (exportScope, error) {
	sc := exportScope{key: map[string]string{}}
	v := s.viewer(r)
	general := offices.Office{ID: offices.GeneralID, Name: "General", General: true}
	if r.URL.Query().Get("scope") == "company" {
		if !v.Admin {
			return sc, fmt.Errorf("%w: only an administrator saves the whole company as a template", domain.ErrForbidden)
		}
		sc.kind = ot.KindCompany
		if m == nil {
			sc.offices = []offices.Office{general}
		}
		if m != nil {
			for _, o := range m.Offices() {
				if visibleOffice(m, v, o) { // a private department the admin is not in stays out (D3)
					sc.offices = append(sc.offices, o)
				}
			}
		}
	} else {
		id := strings.TrimSpace(r.URL.Query().Get("office_id"))
		if id == "" {
			id = offices.GeneralID
		}
		sc.kind = ot.KindDepartment
		o := general
		if m != nil {
			var ok bool
			if o, ok = m.Office(id); !ok || !visibleOffice(m, v, o) {
				return sc, fmt.Errorf("%w: department %q", domain.ErrNotFound, id) // hidden = absent
			}
			if !m.CanManage(v, id) {
				return sc, fmt.Errorf("%w: only its responsible or an administrator saves a department as a template", domain.ErrForbidden)
			}
		} else if id != offices.GeneralID {
			return sc, fmt.Errorf("%w: department %q", domain.ErrNotFound, id)
		} else if !v.Admin {
			return sc, fmt.Errorf("%w: only an administrator saves General as a template", domain.ErrForbidden)
		}
		sc.offices = []offices.Office{o}
	}
	n := 0
	for _, o := range sc.offices {
		if o.General || o.ID == offices.GeneralID {
			sc.key[o.ID] = ot.GeneralKey
			continue
		}
		n++
		sc.key[o.ID] = fmt.Sprintf("d%d", n)
	}
	return sc, nil
}

func (s *server) exportOfficeTemplate(w http.ResponseWriter, r *http.Request) {
	m, err := s.teamModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	sc, err := s.exportScopeOf(r, m)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.buildOfficeTemplate(r, m, sc)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "office_template.exported", Entity: "office_template", EntityID: sc.kind,
			Details: map[string]any{"kind": sc.kind, "offices": len(sc.offices), "agents": len(out.Template.Agents), "skills": len(out.Template.Skills),
				"workflows": len(out.Template.Workflows), "schedules": len(out.Template.Schedules), "goals": len(out.Template.Goals), "left_out": len(out.LeftOut)}})
	}
	writeJSON(w, http.StatusOK, out)
}

// buildOfficeTemplate drafts the file field by field from the organization (allow-list), then Scrub and Validate.
func (s *server) buildOfficeTemplate(r *http.Request, m *offices.Model, sc exportScope) (otExportView, error) {
	ctx := r.Context()
	t := ot.Template{Format: ot.Format, FormatVersion: ot.FormatVersion, Kind: sc.kind}
	leftOut := []string{}
	for _, o := range sc.offices {
		t.Departments = append(t.Departments, ot.Department{Key: sc.key[o.ID], Name: o.Name})
	}
	if sc.kind == ot.KindCompany {
		t.Name = "Empresa"
		if s.Auth != nil {
			if p, ok := auth.PrincipalFrom(ctx); ok {
				if orgs, err := s.Auth.Orgs(ctx, p.UserID); err == nil {
					for _, o := range orgs {
						if o.OrgID == s.org(r) && strings.TrimSpace(o.Name) != "" {
							t.Name = o.Name
						}
					}
				}
			}
		}
	} else {
		t.Name = sc.offices[0].Name
	}
	all, err := s.Store.ListAgents(ctx, s.org(r))
	if err != nil {
		return otExportView{}, err
	}
	roleOf := map[string]string{}
	for _, a := range all {
		roleOf[a.ID] = a.Role
	}
	am, v, err := s.agentModel(r)
	if err != nil {
		return otExportView{}, err
	}
	officeOf := func(id string) string {
		if m == nil {
			return offices.GeneralID
		}
		return m.Agent(id).OfficeID
	}
	exported, hidden := []domain.Agent{}, 0
	for _, a := range all {
		if _, in := sc.key[officeOf(a.ID)]; !in {
			continue
		}
		if am != nil && !am.Rights(v, a.ID).See {
			hidden++ // the private agent of another person
			continue
		}
		exported = append(exported, a)
	}
	if hidden > 0 {
		leftOut = append(leftOut, fmt.Sprintf("%d private agent(s) of other people", hidden))
	}
	skillIDs, skillSeen := []string{}, map[string]bool{}
	sa := s.skillActor(r)
	roles := map[string]bool{}
	for _, a := range exported {
		roles[a.Role] = true
		ta := ot.Agent{Department: sc.key[officeOf(a.ID)], Role: a.Role, Name: a.Name}
		if s.Profiles != nil {
			if p, err := s.Profiles.Get(ctx, a.ID); err == nil {
				ta.Instructions, ta.Creativity, ta.Tone, ta.Tags = p.Instructions, p.Creativity, p.Tone, slices.Clone(p.Skills)
			}
		}
		if s.OrgChart != nil {
			if j, err := s.OrgChart.Job(ctx, a.ID); err == nil {
				ta.Responsibilities = slices.Clone(j.Responsibilities)
			}
		}
		if s.Skills != nil {
			if list, err := s.Skills.AgentSkills(ctx, a.ID); err == nil {
				for _, as := range list {
					if as.Via != skills.ViaAgent {
						continue // the role's defaults come with the role in the other company too
					}
					ta.Skills = append(ta.Skills, as.Name)
					if !skillSeen[as.ID] {
						skillSeen[as.ID] = true
						skillIDs = append(skillIDs, as.ID)
					}
				}
			}
		}
		t.Agents = append(t.Agents, ta)
	}
	if len(t.Agents) == 0 {
		return otExportView{}, fmt.Errorf("%w: there is nobody to save in this template", domain.ErrInvalid)
	}
	for _, id := range skillIDs {
		pkg, sk, ok, err := s.Skills.ActivePackage(ctx, sa, id)
		if err != nil || !ok {
			if sk.Name != "" && !sk.Builtin {
				leftOut = append(leftOut, fmt.Sprintf("skill %s: it has no approved version", sk.Name))
			}
			continue
		}
		ts := ot.Skill{Name: sk.Name, SkillMD: pkg.SkillMD}
		for _, f := range pkg.Files {
			ts.Files = append(ts.Files, ot.SkillFile{Path: f.Path, Content: f.Content})
		}
		t.Skills = append(t.Skills, ts)
	}
	if s.Projects != nil {
		tpls, err := s.Projects.Templates(ctx)
		if err != nil {
			return otExportView{}, err
		}
		for _, pt := range tpls {
			if pt.Builtin || !pt.Shared || !strings.HasPrefix(pt.ID, "tpl-") {
				continue // built-ins exist everywhere; a private template is its author's
			}
			wf := ot.FromProjectTemplate(pt, func(id string) string { return roleOf[id] })
			if sc.kind == ot.KindDepartment && !slices.ContainsFunc(wf.Roles(), func(r string) bool { return roles[r] }) {
				continue
			}
			t.Workflows = append(t.Workflows, wf)
		}
	}
	if s.OrgConfig != nil {
		if list, err := s.OrgConfig.ListSchedules(ctx); err == nil {
			for _, sch := range list {
				if sc.kind == ot.KindDepartment {
					wf, ok := s.teamCatalog().Workflow(sch.TemplateKey, "es")
					if !ok || !slices.ContainsFunc(wf.Steps, func(st catalog.StepView) bool { return roles[st.AgentID] }) {
						continue
					}
				}
				t.Schedules = append(t.Schedules, ot.Schedule{Template: sch.TemplateKey, Params: sch.Params, Hour: sch.Hour,
					Minute: sch.Minute, Timezone: sch.Timezone, Weekdays: slices.Clone(sch.Weekdays)})
			}
		}
	}
	if s.Goals != nil {
		if list, err := s.Goals.List(ctx, s.org(r), s.goalActor(r), false); err == nil {
			for _, g := range list {
				if g.Status != goals.StatusActive {
					continue
				}
				key := ""
				if g.OfficeID != "" {
					var in bool
					if key, in = sc.key[g.OfficeID]; !in {
						continue
					}
				} else if sc.kind == ot.KindDepartment && sc.offices[0].ID != offices.GeneralID {
					continue // a company goal belongs to the company, not to a department
				}
				tg := ot.Goal{Department: key, Title: g.Title, Description: g.Description, Period: g.Period}
				if g.DueDate != nil {
					tg.DueDate = g.DueDate.Format("2006-01-02")
				}
				t.Goals = append(t.Goals, tg)
			}
		}
	}
	leftOut = append(leftOut, ot.Scrub(&t, s.teamCatalog())...)
	if err := ot.Validate(&t, s.teamCatalog()); err != nil {
		return otExportView{}, err
	}
	return otExportView{FileName: fileSlug(t.Name) + ".aiw-template.json", Template: t, LeftOut: leftOut}, nil
}

func fileSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "plantilla"
	}
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

// ---------------------------------------------------------------- import

func (s *server) importOfficeTemplate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, ot.MaxFileBytes+1)
	data, err := io.ReadAll(r.Body)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) || len(data) > ot.MaxFileBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "file_too_large", "error": "the template is larger than 3 MB"})
		return
	}
	if err != nil {
		s.fail(w, errors.Join(domain.ErrInvalid, err))
		return
	}
	t, err := ot.Parse(data, s.teamCatalog())
	if errors.Is(err, ot.ErrTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "file_too_large", "error": "the template is larger than 3 MB"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "template_invalid", "error": err.Error()})
		return
	}
	st, created, err := s.OfficeTemplates.Save(r.Context(), s.org(r), s.actor(r), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	if created && s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "office_template.imported", Entity: "office_template", EntityID: st.ID,
			Details: map[string]any{"kind": t.Kind, "format_version": t.FormatVersion, "agents": len(t.Agents), "skills": len(t.Skills),
				"workflows": len(t.Workflows), "schedules": len(t.Schedules), "goals": len(t.Goals)}})
	}
	p, err := s.officeTemplatePreview(r, st, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, p)
}

func (s *server) deleteOfficeTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.OfficeTemplates.Store.DeleteTemplate(r.Context(), s.org(r), id); err != nil {
		s.fail(w, err)
		return
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "office_template.deleted", Entity: "office_template", EntityID: id})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- preview

type otDeptView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Destination is where its people go here: the department with the same name the person manages, the one chosen,
	// or General. Matched says it was found by name.
	Destination struct {
		OfficeID   string `json:"office_id"`
		OfficeName string `json:"office_name"`
		Matched    bool   `json:"matched"`
	} `json:"destination"`
	application.TeamPreview
}

type otItemView struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "new" or "existing"; goals: "active", "archived" or "existing"
	Detail string `json:"detail,omitempty"`
}

type otPreviewView struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Kind          string       `json:"kind"`
	For           string       `json:"for"`
	Solves        string       `json:"solves"`
	FormatVersion int          `json:"format_version"`
	OfficeID      string       `json:"office_id"` // the destination chosen for what has no department of its name
	Departments   []otDeptView `json:"departments"`
	Skills        []otItemView `json:"skills"`
	Workflows     []otItemView `json:"workflows"`
	Schedules     []otItemView `json:"schedules"`
	Goals         []otItemView `json:"goals"`
	NewCount      int          `json:"new_count"`
	ExistingCount int          `json:"existing_count"`
	CanInstall    bool         `json:"can_install"`
	BlockedReason string       `json:"blocked_reason,omitempty"` // "nothing_new", "office_full" or "plan_limit"
}

// otPlan is a template resolved against this organization: where each department goes and who is new.
type otPlan struct {
	view   otPreviewView
	dests  map[string]teamDest // template department key -> destination
	agents []domain.Agent
}

func (s *server) officeTemplateDest(r *http.Request, m *offices.Model, t ot.Template, key, requested string) (teamDest, error) {
	tv := t.TeamFor(key)
	if key == ot.GeneralKey {
		return s.teamDestination(r, m, tv, offices.GeneralID)
	}
	if t.Kind == ot.KindDepartment {
		return s.teamDestination(r, m, tv, requested)
	}
	d, err := s.teamDestination(r, m, tv, "")
	if err == nil && d.Suggested == "" && requested != "" {
		return s.teamDestination(r, m, tv, requested)
	}
	return d, err
}

func (s *server) officeTemplatePlan(r *http.Request, st ot.Stored, requested string) (otPlan, error) {
	ctx := r.Context()
	t := st.Template
	plan := otPlan{dests: map[string]teamDest{}}
	m, err := s.teamModel(r)
	if err != nil {
		return plan, err
	}
	if plan.agents, err = s.Store.ListAgents(ctx, s.org(r)); err != nil {
		return plan, err
	}
	p := otPreviewView{ID: st.ID, Name: t.Name, Kind: t.Kind, For: t.For, Solves: t.Solves, FormatVersion: t.FormatVersion,
		OfficeID: offices.GeneralID, Departments: []otDeptView{}, Skills: []otItemView{}, Workflows: []otItemView{},
		Schedules: []otItemView{}, Goals: []otItemView{}}
	if requested != "" {
		// the chosen destination must exist for the person and be theirs to manage, whatever the template uses it for
		if _, err := s.teamDestination(r, m, catalog.TeamView{}, requested); err != nil {
			return plan, err
		}
		p.OfficeID = requested
	}
	loc := s.orgLang(ctx)
	// the names suggested in one department must not repeat in the next: earlier suggestions count as taken
	taken := slices.Clone(plan.agents)
	for _, d := range t.Departments {
		dest, err := s.officeTemplateDest(r, m, t, d.Key, requested)
		if err != nil {
			return plan, err
		}
		plan.dests[d.Key] = dest
		dv := otDeptView{Key: d.Key, Name: d.Name}
		dv.Destination.OfficeID, dv.Destination.OfficeName = dest.ID, dest.Name
		dv.Destination.Matched = dest.Suggested != "" && dest.Suggested == dest.ID
		dv.TeamPreview = application.PreviewTeam(t.TeamFor(d.Key), taken, dest.officeOf, dest.ID, loc)
		s.maskHiddenExisting(r, &dv.TeamPreview)
		for i, ta := range t.AgentsOf(d.Key) {
			if i < len(dv.Agents) && dv.Agents[i].Existing != nil {
				// an agent that fills a place here does not fill a place of another department of the template too
				for j := range taken {
					if taken[j].ID == dv.Agents[i].Existing.ID {
						taken[j].Role = ""
					}
				}
			}
			if i < len(dv.Agents) && dv.Agents[i].Status == application.TeamNew {
				if ta.Name != "" && !nameTaken(taken, ta.Name) {
					dv.Agents[i].Name = ta.Name // the name the person had, kept when free here
				}
				taken = append(taken, domain.Agent{ID: "~" + dv.Agents[i].Name, Name: dv.Agents[i].Name})
			}
		}
		p.NewCount += dv.NewCount
		p.ExistingCount += dv.ExistingCount
		p.Departments = append(p.Departments, dv)
	}
	sa := s.skillActor(r)
	for _, sk := range t.Skills {
		it := otItemView{Name: sk.Name, Status: application.TeamNew, Detail: fmt.Sprintf("%d", len(sk.Files)+1)}
		if s.Skills != nil {
			if _, ok, err := s.Skills.ByName(ctx, sa, sk.Name); err == nil && ok {
				it.Status = application.TeamExisting
			}
		}
		p.Skills = append(p.Skills, it)
	}
	existingTpl := map[string]bool{}
	if s.Projects != nil {
		if tpls, err := s.Projects.Templates(ctx); err == nil {
			for _, pt := range tpls {
				if !pt.Builtin {
					existingTpl[strings.ToLower(strings.TrimSpace(pt.Name))] = true
				}
			}
		}
	}
	for _, wf := range t.Workflows {
		it := otItemView{Name: wf.Name, Status: application.TeamNew}
		if existingTpl[strings.ToLower(wf.Name)] {
			it.Status = application.TeamExisting
		}
		p.Workflows = append(p.Workflows, it)
	}
	var schedules []application.Schedule
	if s.OrgConfig != nil {
		schedules, _ = s.OrgConfig.ListSchedules(ctx)
	}
	for _, sc := range t.Schedules {
		name := sc.Template
		if wf, ok := s.teamCatalog().Workflow(sc.Template, s.teamTextLocale(r)); ok {
			name = wf.Name
		}
		it := otItemView{Name: name, Status: application.TeamNew, Detail: fmt.Sprintf("%02d:%02d %s", sc.Hour, sc.Minute, sc.Timezone)}
		if slices.ContainsFunc(schedules, func(x application.Schedule) bool { return sameSchedule(x, sc) }) {
			it.Status = application.TeamExisting
		}
		p.Schedules = append(p.Schedules, it)
	}
	var have []goals.Goal
	if s.Goals != nil {
		have, _ = s.Goals.List(ctx, s.org(r), s.goalActor(r), true)
	}
	room := goals.MaxActive
	for _, g := range have {
		if g.Status == goals.StatusActive {
			room--
		}
	}
	for _, g := range t.Goals {
		office := s.goalOffice(plan.dests, g)
		it := otItemView{Name: g.Title, Status: goals.StatusActive}
		if slices.ContainsFunc(have, func(x goals.Goal) bool { return x.OfficeID == office && strings.EqualFold(x.Title, g.Title) }) {
			it.Status = application.TeamExisting
		} else if room <= 0 {
			it.Status = goals.StatusArchived
		} else {
			room--
		}
		p.Goals = append(p.Goals, it)
	}
	if p.NewCount == 0 && !slices.ContainsFunc(slices.Concat(p.Skills, p.Workflows, p.Schedules), func(i otItemView) bool { return i.Status == application.TeamNew }) &&
		!slices.ContainsFunc(p.Goals, func(i otItemView) bool { return i.Status != application.TeamExisting }) {
		p.BlockedReason = "nothing_new"
	} else if r, why := s.agentRoom(r, len(plan.agents)); r < p.NewCount {
		p.BlockedReason = why
	}
	p.CanInstall = p.BlockedReason == ""
	plan.view = p
	return plan, nil
}

func nameTaken(agents []domain.Agent, name string) bool {
	return slices.ContainsFunc(agents, func(a domain.Agent) bool { return strings.EqualFold(a.Name, name) })
}

func sameSchedule(x application.Schedule, s ot.Schedule) bool {
	return x.TemplateKey == s.Template && x.Hour == s.Hour && x.Minute == s.Minute && x.Timezone == s.Timezone && slices.Equal(x.Weekdays, s.Weekdays)
}

// goalOffice is the department a goal of the template lands in ("" = the company).
func (s *server) goalOffice(dests map[string]teamDest, g ot.Goal) string {
	if g.Department == "" {
		return ""
	}
	if d, ok := dests[g.Department]; ok && d.ID != offices.GeneralID {
		return d.ID
	}
	return ""
}

func (s *server) officeTemplatePreview(r *http.Request, st ot.Stored, requested string) (otPreviewView, error) {
	plan, err := s.officeTemplatePlan(r, st, requested)
	return plan.view, err
}

type otCardView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	For           string   `json:"for"`
	Solves        string   `json:"solves"`
	Departments   []string `json:"departments"`
	Agents        int      `json:"agents"`
	Skills        int      `json:"skills"`
	Workflows     int      `json:"workflows"`
	Goals         int      `json:"goals"`
	NewCount      int      `json:"new_count"`
	ExistingCount int      `json:"existing_count"`
}

func (s *server) listOfficeTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.OfficeTemplates.Store.ListTemplates(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []otCardView{}
	for _, st := range list {
		t := st.Template
		c := otCardView{ID: st.ID, Name: t.Name, Kind: t.Kind, For: t.For, Solves: t.Solves, Departments: []string{}, Agents: len(t.Agents),
			Skills: len(t.Skills), Workflows: len(t.Workflows), Goals: len(t.Goals)}
		for _, d := range t.Departments {
			c.Departments = append(c.Departments, d.Name)
		}
		if p, err := s.officeTemplatePreview(r, st, ""); err == nil {
			c.NewCount, c.ExistingCount = p.NewCount, p.ExistingCount
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getOfficeTemplate(w http.ResponseWriter, r *http.Request) {
	st, err := s.OfficeTemplates.Store.GetTemplate(r.Context(), s.org(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.officeTemplatePreview(r, st, strings.TrimSpace(r.URL.Query().Get("office_id")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---------------------------------------------------------------- install

type otInstallResult struct {
	Created   []domain.Agent `json:"created"`
	Skills    []string       `json:"skills"`    // created, waiting for review
	Workflows []string       `json:"workflows"` // work templates created
	Schedules int            `json:"schedules"` // created switched off
	Goals     []otItemView   `json:"goals"`     // created, active or archived
	// Skipped is what could not be installed, in plain words (the rest was).
	Skipped []string      `json:"skipped"`
	Preview otPreviewView `json:"preview"`
}

// installOfficeTemplate installs what the organization lacks, by the same path as a ready-to-hire team: HireForRole
// (no connection, the role's starting autonomy under its ceiling: trust is earned again here), the office and plan
// caps for every new person before anyone is hired, the per-organization lock and AssignAgent. Then the profile of each
// new person, its skills (created in review, never active), the work templates, the schedules (switched off) and the
// goals (active only when they fit). Installing again creates nothing twice.
func (s *server) installOfficeTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OfficeID string `json:"office_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if s.Orch == nil {
		s.fail(w, fmt.Errorf("%w: this deployment cannot hire agents", domain.ErrInvalid))
		return
	}
	ctx, org := r.Context(), s.org(r)
	st, err := s.OfficeTemplates.Store.GetTemplate(ctx, org, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	t := st.Template
	mu, _ := teamInstallLocks.LoadOrStore(org, &sync.Mutex{}) // the same lock as the teams: one install at a time
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	requested := strings.TrimSpace(body.OfficeID)
	plan, err := s.officeTemplatePlan(r, st, requested)
	if err != nil {
		s.fail(w, err)
		return
	}
	newCount := plan.view.NewCount
	if len(plan.agents)+newCount > maxAgentsPerOrg {
		s.fail(w, fmt.Errorf("%w: the office is full (%d agents)", domain.ErrConflict, maxAgentsPerOrg))
		return
	}
	if pl := s.planLimits(ctx, org); pl.MaxAgents > 0 && newCount > 0 && policy.AtLimit(len(plan.agents)+newCount-1, pl.MaxAgents) {
		s.fail(w, &domain.PlanLimitError{Kind: policy.LimitAgents, Limit: pl.MaxAgents, Plan: pl.ID})
		return
	}
	res := otInstallResult{Created: []domain.Agent{}, Skills: []string{}, Workflows: []string{}, Goals: []otItemView{}, Skipped: []string{}}
	loc := s.orgLang(ctx)
	// who does each role here, for the steps of the work templates: the people of the template first
	roleAgent := map[string]string{}
	// the new agents of each skill of the file, and the offices they are in
	skillAgents, skillOffices := map[string][]string{}, map[string]map[string]bool{}
	for _, dv := range plan.view.Departments {
		dest := plan.dests[dv.Key]
		for i, ta := range t.AgentsOf(dv.Key) {
			if i >= len(dv.Agents) {
				break
			}
			pa := dv.Agents[i]
			if pa.Status != application.TeamNew {
				if pa.Existing != nil && pa.Existing.ID != "" && roleAgent[ta.Role] == "" {
					roleAgent[ta.Role] = pa.Existing.ID
				}
				continue // an agent already here keeps what it has
			}
			a, err := s.Orch.HireForRoleNamed(ctx, ta.Role, loc, pa.Name)
			if err != nil {
				s.fail(w, err)
				return
			}
			res.Created = append(res.Created, a)
			if roleAgent[ta.Role] == "" {
				roleAgent[ta.Role] = a.ID
			}
			if dest.ID != offices.GeneralID && s.OrgChart != nil {
				if err := s.OrgChart.AssignAgent(ctx, a.ID, dest.ID); err != nil {
					s.fail(w, err)
					return
				}
			}
			res.Skipped = append(res.Skipped, s.applyTemplateAgent(r, a, ta)...)
			for _, sk := range ta.Skills {
				skillAgents[sk] = append(skillAgents[sk], a.ID)
				if skillOffices[sk] == nil {
					skillOffices[sk] = map[string]bool{}
				}
				skillOffices[sk][dest.ID] = true
			}
		}
	}
	if s.Skills != nil {
		sa := s.skillActor(r)
		inFile := map[string]ot.Skill{}
		for _, sk := range t.Skills {
			inFile[sk.Name] = sk
		}
		names := []string{}
		for n := range skillAgents {
			names = append(names, n)
		}
		for _, sk := range t.Skills {
			if _, used := skillAgents[sk.Name]; !used {
				names = append(names, sk.Name) // a skill of the file nobody new uses still arrives, for review
			}
		}
		slices.Sort(names)
		names = slices.Compact(names)
		for _, name := range names {
			id := ""
			if have, ok, err := s.Skills.ByName(ctx, sa, name); err == nil && ok {
				id = have.ID
			} else if sk, ok := inFile[name]; ok {
				files := make([]skills.File, 0, len(sk.Files))
				for _, f := range sk.Files {
					files = append(files, skills.File{Path: f.Path, Content: f.Content})
				}
				pkg, err := skills.Build(sk.SkillMD, files)
				if err != nil {
					res.Skipped = append(res.Skipped, "skill "+name+": "+err.Error())
					continue
				}
				office := ""
				if len(skillOffices[name]) == 1 { // everybody who uses it is in one department: it is that department's
					for o := range skillOffices[name] {
						if o != offices.GeneralID {
							office = o
						}
					}
				}
				d, err := s.Skills.ImportPackage(ctx, sa, pkg, office)
				if err != nil {
					res.Skipped = append(res.Skipped, "skill "+name+": "+err.Error())
					continue
				}
				id = d.ID
				res.Skills = append(res.Skills, name)
			}
			if id == "" {
				if len(skillAgents[name]) > 0 {
					res.Skipped = append(res.Skipped, "skill "+name+": not in this organization; not assigned")
				}
				continue
			}
			for _, agentID := range skillAgents[name] {
				if err := s.Skills.Assign(ctx, sa, agentID, id); err != nil {
					res.Skipped = append(res.Skipped, "skill "+name+": "+err.Error())
				}
			}
		}
	}
	if s.Projects != nil && len(t.Workflows) > 0 {
		agents, _ := s.Store.ListAgents(ctx, org)
		ix := application.RoleAgents(agents)
		agentOf := func(role string) string {
			if id := roleAgent[role]; id != "" {
				return id
			}
			return ix.AgentFor(role)
		}
		for i, wf := range t.Workflows {
			if plan.view.Workflows[i].Status != application.TeamNew {
				continue
			}
			if _, err := s.Projects.CreateTemplate(ctx, wf.ToProjectInput(agentOf)); err != nil {
				res.Skipped = append(res.Skipped, "work template "+wf.Name+": "+err.Error())
				continue
			}
			res.Workflows = append(res.Workflows, wf.Name)
		}
	}
	if s.OrgConfig != nil {
		off := false
		for i, sc := range t.Schedules {
			if plan.view.Schedules[i].Status != application.TeamNew {
				continue
			}
			if _, err := s.OrgConfig.CreateSchedule(ctx, application.ScheduleInput{TemplateKey: sc.Template, Params: sc.Params, Hour: sc.Hour,
				Minute: sc.Minute, Timezone: sc.Timezone, Weekdays: sc.Weekdays, Enabled: &off}); err != nil {
				res.Skipped = append(res.Skipped, "schedule "+sc.Template+": "+err.Error())
				continue
			}
			res.Schedules++
		}
	}
	if s.Goals != nil {
		for i, g := range t.Goals {
			if plan.view.Goals[i].Status == application.TeamExisting {
				continue
			}
			made, err := s.Goals.Import(ctx, org, s.goalActor(r), goals.Input{Title: g.Title, Description: g.Description, Period: g.Period,
				DueDate: g.DueDate, OfficeID: s.goalOffice(plan.dests, g)})
			if err != nil {
				res.Skipped = append(res.Skipped, "goal "+g.Title+": "+err.Error())
				continue
			}
			res.Goals = append(res.Goals, otItemView{Name: made.Title, Status: made.Status})
		}
	}
	if s.Rec != nil {
		ids := make([]string, 0, len(res.Created))
		for _, a := range res.Created {
			ids = append(ids, a.ID)
		}
		s.Rec.Audit(ctx, domain.AuditLog{Actor: s.actor(r), Action: "office_template.installed", Entity: "office_template", EntityID: st.ID,
			Details: map[string]any{"format_version": t.FormatVersion, "office_id": plan.view.OfficeID, "created": ids, "already_there": plan.view.ExistingCount,
				"skills": len(res.Skills), "workflows": len(res.Workflows), "schedules": res.Schedules, "goals": len(res.Goals), "skipped": len(res.Skipped)}})
	}
	if res.Preview, err = s.officeTemplatePreview(r, st, requested); err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if len(res.Created)+len(res.Skills)+len(res.Workflows)+res.Schedules+len(res.Goals) > 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}

// applyTemplateAgent gives a newly hired agent what the template says it made its own: the work profile (extra
// instructions, creativity, tone, tags) and the responsibilities of its job. Autonomy is never touched.
func (s *server) applyTemplateAgent(r *http.Request, a domain.Agent, ta ot.Agent) []string {
	ctx, skipped := r.Context(), []string{}
	if s.Profiles != nil && (ta.Instructions != "" || ta.Creativity != "" || ta.Tone != "" || len(ta.Tags) > 0) {
		tone := ta.Tone
		if _, err := s.Profiles.Update(ctx, a.ID, application.ProfileInput{Instructions: ta.Instructions, Creativity: ta.Creativity,
			Skills: ta.Tags, Tone: &tone}); err != nil {
			skipped = append(skipped, a.Name+": "+err.Error())
		}
	} else if ta.Tone != "" && s.OrgConfig != nil {
		if err := s.OrgConfig.SetAgentTone(ctx, a.ID, ta.Tone); err != nil {
			skipped = append(skipped, a.Name+": "+err.Error())
		}
	}
	if s.OrgChart != nil && len(ta.Responsibilities) > 0 {
		if j, err := s.OrgChart.Job(ctx, a.ID); err == nil && !slices.Equal(j.Responsibilities, ta.Responsibilities) {
			if _, err := s.OrgChart.PutJob(ctx, a.ID, jobWith(j, ta.Responsibilities)); err != nil {
				skipped = append(skipped, a.Name+": "+err.Error())
			}
		}
	}
	return skipped
}

// jobWith is the job description j with other responsibilities (everything else stays).
func jobWith(j orgchart.Job, resp []string) orgchart.JobInput {
	return orgchart.JobInput{DepartmentID: j.DepartmentID, ReportsTo: j.ReportsTo, Escalation: j.Escalation, Responsibilities: resp,
		AllowedTools: j.AllowedTools, Schedule: j.Schedule, BudgetUSD: j.BudgetUSD}
}
