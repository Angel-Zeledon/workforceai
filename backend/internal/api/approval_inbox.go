package api

import (
	"context"
	"net/http"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// getApprovalInbox: GET /approvals/inbox (approvals:read), the cross-project inbox of B17
// (docs/architecture/approvals-inbox.md). Read-only: deciding stays on POST /approvals/{id}/decision and
// POST /approvals/batch. The devil's advocate summary is only read from its cache, never generated here.
func (s *server) getApprovalInbox(w http.ResponseWriter, r *http.Request) {
	ctx, org := r.Context(), s.org(r)
	pending, err := s.Store.ListApprovals(ctx, org, string(domain.ApprovalPending))
	if err != nil {
		s.fail(w, err)
		return
	}
	history, err := s.Store.ListApprovals(ctx, org, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	tasks := map[string]domain.Task{}
	for i := range pending {
		pending[i].Context = s.approvalContext(pending[i].ID)
		if _, seen := tasks[pending[i].TaskID]; seen || pending[i].TaskID == "" {
			continue
		}
		if t, err := s.Store.GetTask(ctx, org, pending[i].TaskID); err == nil {
			tasks[t.ID] = t
		}
	}
	in := application.InboxInput{Pending: pending, History: history, Tasks: tasks, Now: time.Now().UTC()}
	if s.Projects != nil {
		in.ProjectOf = func(requestID string) string { return s.Projects.ProjectIDForRequest(ctx, org, requestID) }
		var names map[string]string
		in.ProjectName = func(id string) string {
			if names == nil {
				names = projectNames(ctx, s)
			}
			return names[id]
		}
	}
	if s.DevilsAdvocate != nil {
		in.Counter = func(id string) (domain.Counterargument, bool) {
			c, err := s.DevilsAdvocate.Get(ctx, id)
			return c, err == nil
		}
	}
	writeJSON(w, http.StatusOK, application.BuildApprovalInbox(in))
}

func projectNames(ctx context.Context, s *server) map[string]string {
	out := map[string]string{}
	list, err := s.Projects.List(ctx)
	if err != nil {
		return out
	}
	for _, p := range list {
		out[p.ID] = p.Name
	}
	return out
}
