package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/filecomments"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// wireFileComments builds the comments anchored to a passage of a file (F7, docs/architecture/anchored-comments.md)
// and plugs the drive's version hook into it: when the agent that was asked for a change saves a new version, the
// comment waiting for it is resolved with a link to that version. Every change is audited (metadata only).
func wireFileComments(pg *postgres.Store, rec *application.Recorder, dr *drive.Service, orgID string) *filecomments.Service {
	if dr == nil {
		return nil
	}
	var st filecomments.Store = filecomments.NewMemStore()
	if pg != nil {
		st = &postgres.FileCommentStore{S: pg}
	}
	svc := filecomments.New(filecomments.Config{Store: st, Drive: dr, OrgID: orgID,
		Audit: func(ctx context.Context, org string, a filecomments.Actor, action, id string, d map[string]any) {
			det := map[string]any{"actor_kind": a.Kind}
			for k, v := range d {
				det[k] = v
			}
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: a.ID, Action: action, Entity: "file_comment", EntityID: id, Details: det})
		}})
	dr.SetVersionHook(svc.OnVersion)
	return svc
}
