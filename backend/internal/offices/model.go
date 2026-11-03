package offices

import (
	"slices"
	"sort"
)

// Model is the computed picture of one organization: its offices (General first), who is in each one and the
// effective access of every agent. It is read-only and cached for a few seconds by the Service.
type Model struct {
	offices    []Office
	byID       map[string]*Office
	people     map[string]Person
	admins     []string
	member     map[string]map[string]bool // office -> user (General is implicit)
	lead       map[string]map[string]bool // office -> user (explicit leads)
	access     map[string]EffectiveAccess
	agents     []AgentRef
	rows       map[string]AgentAccess
	restricted bool
	depts      []Dept
}

func buildModel(depts []Dept, agentDept map[string]string, people []Person, agents []AgentRef,
	rows []OfficeRow, members []MemberRow, access []AgentAccess) *Model {
	m := &Model{byID: map[string]*Office{}, people: map[string]Person{}, member: map[string]map[string]bool{},
		lead: map[string]map[string]bool{}, access: map[string]EffectiveAccess{}, agents: agents, rows: map[string]AgentAccess{}, depts: depts}
	ids := make([]string, 0, len(people))
	for _, p := range people {
		m.people[p.UserID] = p
		ids = append(ids, p.UserID)
		if p.Admin {
			m.admins = append(m.admins, p.UserID)
		}
	}
	sort.Strings(ids)
	sort.Strings(m.admins)
	m.offices = append(m.offices, Office{ID: GeneralID, Name: "General", Visibility: Open, General: true, Leads: slices.Clone(m.admins), Members: ids})

	deptByID := map[string]Dept{}
	for _, d := range depts {
		deptByID[d.ID] = d
	}
	for _, r := range rows {
		d, ok := deptByID[r.DepartmentID]
		if !ok {
			continue // the department was deleted: so is its office
		}
		m.offices = append(m.offices, Office{ID: d.ID, Name: d.Name, Visibility: r.Visibility, CreatedAt: r.CreatedAt})
		m.member[d.ID], m.lead[d.ID] = map[string]bool{}, map[string]bool{}
	}
	for _, r := range members {
		if _, ok := m.member[r.OfficeID]; !ok {
			continue
		}
		if _, ok := m.people[r.UserID]; !ok {
			continue // they left the organization
		}
		m.member[r.OfficeID][r.UserID] = true
		if r.Role == RoleLead {
			m.lead[r.OfficeID][r.UserID] = true
		}
	}
	for i := range m.offices {
		o := &m.offices[i]
		if !o.General {
			o.Members, o.Leads = keys(m.member[o.ID]), keys(m.lead[o.ID])
			if len(o.Leads) == 0 {
				o.Leads = slices.Clone(m.admins) // never without a responsible
			}
		}
		m.byID[o.ID] = o
	}
	m.restricted = len(m.offices) > 1 || len(access) > 0

	for _, a := range access {
		m.rows[a.AgentID] = a
	}
	for _, ag := range agents {
		m.access[ag.ID] = m.effective(ag.ID, agentDept[ag.ID])
	}
	return m
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// officeOr returns id when it names an office of the model, else General.
func (m *Model) officeOr(id string) string {
	if _, ok := m.byID[id]; ok && id != "" {
		return id
	}
	return GeneralID
}

func (m *Model) effective(agentID, dept string) EffectiveAccess {
	row, ok := m.rows[agentID]
	if !ok {
		off := m.officeOr(dept)
		return EffectiveAccess{AgentID: agentID, OwnerKind: OwnerOffice, OwnerID: off, OfficeID: off, Visibility: VisOffice, Shares: []Share{}}
	}
	ea := EffectiveAccess{AgentID: agentID, OwnerKind: row.OwnerKind, OwnerID: row.OwnerID, Visibility: row.Visibility, Explicit: true,
		Shares: slices.Clone(row.Shares)}
	if ea.Shares == nil {
		ea.Shares = []Share{}
	}
	if !validVis(ea.Visibility) {
		ea.Visibility = VisOffice
	}
	switch row.OwnerKind {
	case OwnerPerson:
		ea.OfficeID = m.officeOr(row.HomeOffice)
		if _, here := m.people[row.OwnerID]; !here {
			// the owner left: the agent passes to the responsible of its office, never orphaned
			ea.Departed, ea.PreviousOwner = true, row.OwnerID
			ea.OwnerKind, ea.OwnerID = OwnerOffice, ea.OfficeID
		}
	default:
		ea.OwnerKind = OwnerOffice
		ea.OwnerID = m.officeOr(row.OwnerID)
		ea.OfficeID = ea.OwnerID
	}
	return ea
}

// Departments returns the departments of the org chart.
func (m *Model) Departments() []Dept { return m.depts }

// Offices returns every office, General first.
func (m *Model) Offices() []Office { return m.offices }

// Office returns one office.
func (m *Model) Office(id string) (Office, bool) {
	o, ok := m.byID[id]
	if !ok {
		return Office{}, false
	}
	return *o, true
}

// Restricted reports whether anything differs from "everybody sees and uses every agent".
func (m *Model) Restricted() bool { return m.restricted }

// Person returns a person of the organization.
func (m *Model) Person(id string) (Person, bool) {
	p, ok := m.people[id]
	return p, ok
}

// IsMember reports whether user is a member of the office (everybody is a member of General).
func (m *Model) IsMember(user, office string) bool {
	if office == GeneralID {
		_, ok := m.people[user]
		return ok
	}
	return m.member[office][user]
}

// IsLead reports whether user is a responsible of the office (the admins when it has none, and of General).
func (m *Model) IsLead(user, office string) bool {
	o, ok := m.byID[office]
	return ok && slices.Contains(o.Leads, user)
}

// OfficesOf returns the offices of a person: General first, then the others in order.
func (m *Model) OfficesOf(user string) []string {
	var out []string
	for _, o := range m.offices {
		if m.IsMember(user, o.ID) {
			out = append(out, o.ID)
		}
	}
	return out
}

// CanEnter reports whether the viewer sees the office from inside (an open office or one of theirs).
func (m *Model) CanEnter(v Viewer, office string) bool {
	o, ok := m.byID[office]
	if !ok {
		return false
	}
	return o.Visibility == Open || v.Admin || m.IsMember(v.UserID, office)
}

// CanManage reports whether the viewer manages the office (members, visibility, its agents, its memory).
func (m *Model) CanManage(v Viewer, office string) bool {
	if _, ok := m.byID[office]; !ok {
		return false
	}
	return v.Admin || office != GeneralID && m.IsLead(v.UserID, office)
}

// Agent returns the effective access of an agent (an agent the model does not know yet belongs to General).
func (m *Model) Agent(id string) EffectiveAccess {
	if ea, ok := m.access[id]; ok {
		return ea
	}
	return m.effective(id, "")
}

// Agents returns the agents of the organization.
func (m *Model) Agents() []AgentRef { return m.agents }

// Rights computes what the viewer may do with an agent (the ladder of equipos-oficinas.md §4).
func (m *Model) Rights(v Viewer, agentID string) Rights {
	ea := m.Agent(agentID)
	personal := ea.OwnerKind == OwnerPerson
	owner := personal && ea.OwnerID == v.UserID || !personal && m.IsLead(v.UserID, ea.OfficeID) && ea.OfficeID != GeneralID
	shared, shareEdit := false, false
	for _, s := range ea.Shares {
		if s.UserID == v.UserID {
			shared, shareEdit = true, s.Level == ShareEdit
		}
	}
	// the office counts for "office" and "directory", and for "people" when the office owns the agent: a personal
	// agent shared with some people is only theirs (in an organization without offices its office is everybody)
	officeWide := ea.Visibility == VisOffice || ea.Visibility == VisDirectory || ea.Visibility == VisPeople && !personal
	inOffice := m.IsMember(v.UserID, ea.OfficeID)
	use := owner || shared || officeWide && inOffice || ea.Visibility == VisDirectory
	if v.Admin && (!personal || officeWide) {
		use = true // admins may use everything but the agents a person keeps for themself (and whom they share it with)
	}
	open := m.byID[ea.OfficeID] != nil && m.byID[ea.OfficeID].Visibility == Open
	see := use || v.Admin || officeWide && open
	edit := owner || shareEdit || v.Admin
	return Rights{See: see, Use: use, Edit: edit}
}
