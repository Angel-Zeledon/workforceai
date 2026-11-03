package projects

import (
	"context"

	"aiworkforce/backend/internal/application"
)

// Approval permissions per project and batch (docs/architecture/06-permisos-autonomia.md section 11): the policy
// layer asks which project and batch a request belongs to, so "in this project / batch do not ask for <category>"
// applies only there and only while the project runs.

// ScopeOfRequest returns the project and batch of a request and whether the project still runs.
func (s *Service) ScopeOfRequest(ctx context.Context, org, requestID string) (projectID, batchID string, active bool) {
	rec, err := s.cfg.Store.ByRequest(application.WithOrg(ctx, org), org, requestID)
	if err != nil {
		return "", "", false
	}
	return rec.ID, rec.BatchID, !terminalStatus(rec.Status)
}

// ScopeOfProject returns the batch of a project ("" when it is on its own) and whether the project still runs.
func (s *Service) ScopeOfProject(ctx context.Context, org, projectID string) (batchID string, active bool) {
	rec, err := s.cfg.Store.Get(application.WithOrg(ctx, org), org, projectID)
	if err != nil {
		return "", false
	}
	return rec.BatchID, !terminalStatus(rec.Status)
}
