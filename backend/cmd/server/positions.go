package main

import (
	"context"
	"errors"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/positions"
	"aiworkforce/backend/internal/skills"
)

// wirePositions builds the positions inside a department (docs/architecture/positions.md): Postgres (migration 830)
// when there is a database, memory otherwise. It plugs the library of each agent's position into the drive (what
// fs.search, fs.read and the memory reach) and the position's skills into the skill library. It runs after
// wireDepartments and before orch.Recover (TestDepartmentsAreWiredBeforeRecover).
func wirePositions(pg *postgres.Store, off *offices.Service, sk *skills.Service, drv *drive.Service, rec *application.Recorder, orgID string, log *slog.Logger) *positions.Service {
	if off == nil {
		return nil
	}
	var st positions.Store = positions.NewMemStore()
	if pg != nil {
		st = &postgres.PositionsStore{S: pg}
	}
	cfg := positions.Config{Store: st, Model: off.Model, OrgID: orgID, Log: log,
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "position", EntityID: id, Details: d})
		}}
	if sk != nil {
		cfg.Skills = skillCatalog{svc: sk}
	}
	if drv != nil {
		cfg.Folders = positionFolders{drv: drv}
	}
	svc := positions.New(cfg)
	if drv != nil {
		drv.SetAgentLibraries(svc)
	}
	if sk != nil {
		sk.SetPositionSkills(svc.PositionSkills)
	}
	return svc
}

// skillCatalog lists the skills of the library for the positions.
type skillCatalog struct{ svc *skills.Service }

func (c skillCatalog) Skills(ctx context.Context, org string) ([]positions.SkillInfo, error) {
	l, err := c.svc.Catalog(ctx, org)
	out := make([]positions.SkillInfo, 0, len(l))
	for _, s := range l {
		out = append(out, positions.SkillInfo{ID: s.ID, Name: s.Name, Description: s.Description, OfficeID: s.OfficeID})
	}
	return out, err
}

// positionFolders is the drive side of a department for the positions.
type positionFolders struct{ drv *drive.Service }

func (f positionFolders) Bounds(ctx context.Context, org, officeID string) (positions.Bounds, error) {
	ctx = application.WithOrg(ctx, org)
	b := positions.Bounds{Shared: "/" + drive.RootShared, SharedIn: []string{}}
	folder, err := f.drv.OfficeFolder(ctx, officeID)
	switch {
	case err == nil:
		b.Office = folder.Path
	case !errors.Is(err, domain.ErrNotFound):
		return b, err
	}
	in, err := f.drv.SharedInPaths(ctx, officeID)
	if err != nil {
		return b, err
	}
	b.SharedIn = append(b.SharedIn, in...)
	return b, nil
}

func (f positionFolders) FolderExists(ctx context.Context, org, p string) (bool, error) {
	folder, err := f.drv.FolderByPath(application.WithOrg(ctx, org), p)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return folder.DeletedAt == nil, nil
}
