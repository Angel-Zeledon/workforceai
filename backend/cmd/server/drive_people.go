package main

import (
	"context"

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
	if id := m.Agent(agentID).OfficeID; id != offices.GeneralID {
		return id, nil
	}
	return "", nil
}
