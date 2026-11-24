package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/offices"
)

// officePeople answers drive.PeopleAccess over the offices: a member edits; a non-member reads an open office and
// has no access to a private one, admins included (so it does not use Model.CanEnter, which lets admins in).
type officePeople struct{ svc *offices.Service }

func (o officePeople) OfficeAccess(ctx context.Context, org, userID, officeID string) (string, error) {
	m, err := o.svc.Model(ctx, org)
	if err != nil {
		return drive.AccessNone, err
	}
	if m.IsMember(userID, officeID) {
		return drive.AccessEdit, nil
	}
	off, ok := m.Office(officeID)
	if !ok {
		return drive.AccessNone, nil
	}
	if off.Visibility == offices.Open {
		return drive.AccessRead, nil
	}
	return drive.AccessNone, nil
}

// OfficeMembers implements drive.OfficeMembers: the members of an office (none for an unknown one).
func (o officePeople) OfficeMembers(ctx context.Context, org, officeID string) ([]string, error) {
	m, err := o.svc.Model(ctx, org)
	if err != nil {
		return nil, err
	}
	off, ok := m.Office(officeID)
	if !ok {
		return nil, nil
	}
	return off.Members, nil
}

// fileActivity is the live file.activity event of an agent's action on a file (ctx carries the organization). A path
// inside an office's folder goes only to the members of that office (Audience: such an event is also kept out of the
// organization's event log); when they cannot be told, the event names no file at all (no path: the app ignores it).
func fileActivity(ctx context.Context, svc *drive.Service, agentID string, payload map[string]any) application.Action {
	a := application.Action{Type: "file.activity", AgentID: agentID, Entity: "file", SkipAudit: true, Payload: payload}
	p, _ := payload["path"].(string)
	if p == "" || svc == nil {
		return a
	}
	if members, sealed := svc.SealedAudience(ctx, p); sealed {
		if len(members) == 0 {
			delete(payload, "path")
		} else {
			a.Audience = members
		}
	}
	return a
}

// agentOffices answers drive.AgentOffices over the offices model: the office of the agent, "" for General (General has
// no folder under /Offices). An unreadable model is an error, so the agent's scope is unknown (fail closed).
type agentOffices struct{ svc *offices.Service }

func (a agentOffices) AgentOffice(ctx context.Context, org, agentID string) (string, error) {
	if a.svc == nil {
		return "", nil
	}
	m, err := a.svc.Model(ctx, org)
	if err != nil {
		return "", err
	}
	// the office whose files and memory the agent uses for the request of ctx: that office when it works for it, else
	// its base office (never another one)
	if id := m.WorkOffice(agentID, application.RequestOfficeFrom(ctx)); id != offices.GeneralID {
		return id, nil
	}
	return "", nil
}

// WorksFor and Effective make agentOffices the application.OfficeScope over the offices model.
func (a agentOffices) WorksFor(ctx context.Context, org, agentID, office string) (bool, error) {
	if a.svc == nil {
		return true, nil
	}
	m, err := a.svc.Model(ctx, org)
	if err != nil {
		return false, err
	}
	return m.WorksFor(agentID, office), nil
}

func (a agentOffices) Effective(ctx context.Context, org, agentID string) (string, string, error) {
	if a.svc == nil {
		return "", "", nil
	}
	m, err := a.svc.Model(ctx, org)
	if err != nil {
		return "", "", err
	}
	return m.WorkOffice(agentID, application.RequestOfficeFrom(ctx)), m.Agent(agentID).OfficeID, nil
}

// OfficeRoster implements drive.ShareOffices over the offices model (D4, shares between departments): who belongs to
// an office and who are its responsibles. A person who left is not a member (the model drops them).
func (o officePeople) OfficeRoster(ctx context.Context, org, officeID string) (drive.OfficeRoster, error) {
	m, err := o.svc.Model(ctx, org)
	if err != nil {
		return drive.OfficeRoster{}, err
	}
	off, ok := m.Office(officeID)
	if !ok {
		return drive.OfficeRoster{}, nil
	}
	ro := drive.OfficeRoster{Known: true, General: off.General, Members: off.Members}
	for _, l := range off.Leads {
		if m.IsMember(l, officeID) { // the admins that stand in for a missing lead are not members
			ro.Leads = append(ro.Leads, l)
		}
	}
	return ro, nil
}
