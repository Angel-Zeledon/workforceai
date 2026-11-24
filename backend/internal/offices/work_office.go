package offices

// The office a request is asked from and the office an agent uses for it (design §5, §8).

// WorksFor reports whether the agent works for the office: its base office, or another one that accepted it (only
// while MULTI_OFFICE_AGENTS is on: setExtras drops the rows otherwise). A personal agent works for its person and
// answers true for any office (an office never chooses it, and it keeps its own base context).
func (m *Model) WorksFor(agentID, office string) bool {
	ea := m.Agent(agentID)
	if ea.OwnerKind == OwnerPerson || ea.OfficeID == office {
		return true
	}
	x, ok := m.extra(agentID, office)
	return ok && x.Accepted()
}

// WorkOffice is the office whose memory, files, notes and skills the agent uses for a request asked from `requested`
// ("" = unknown): that office when the agent works for it, else its base office, never another one.
func (m *Model) WorkOffice(agentID, requested string) string {
	ea := m.Agent(agentID)
	if requested == "" || requested == ea.OfficeID || ea.OwnerKind == OwnerPerson {
		return ea.OfficeID
	}
	if x, ok := m.extra(agentID, requested); ok && x.Accepted() {
		return requested
	}
	return ea.OfficeID
}

// CanSeeOfficeWork reports whether the viewer sees what was asked and answered in an office: everything of General
// and of an open office; a private one only its members. An office the model does not know counts as General.
func (m *Model) CanSeeOfficeWork(v Viewer, office string) bool {
	o, ok := m.byID[office]
	if !ok || o.General || o.Visibility != Private {
		return true
	}
	return m.IsMember(v.UserID, office)
}
