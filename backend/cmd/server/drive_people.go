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
	if officeID == offices.GeneralID || m.IsMember(userID, officeID) {
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
