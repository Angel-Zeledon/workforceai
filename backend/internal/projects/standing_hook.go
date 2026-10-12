package projects

import (
	"context"

	"aiworkforce/backend/internal/application"
)

// The projects service answers the standing-approval layer
// (application.ProjectResolver): which project a request belongs to and whether
// it still runs. A finished, failed or cancelled project is not active, so its
// standing approvals stop applying (they expire with the project).
var _ application.ProjectResolver = (*Service)(nil)

// ProjectOfRequest implements application.ProjectResolver.
func (s *Service) ProjectOfRequest(ctx context.Context, org, requestID string) (string, bool) {
	rec, err := s.cfg.Store.ByRequest(application.WithOrg(ctx, org), org, requestID)
	if err != nil {
		return "", false
	}
	return rec.ID, !terminalStatus(rec.Status)
}

// ProjectActive implements application.ProjectResolver.
func (s *Service) ProjectActive(ctx context.Context, org, projectID string) bool {
	rec, err := s.cfg.Store.Get(application.WithOrg(ctx, org), org, projectID)
	return err == nil && !terminalStatus(rec.Status)
}
