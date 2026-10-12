package projects

import (
	"context"

	"aiworkforce/backend/internal/domain"
)

// RewardInfo implements rewards.TaskClassifier: what a finished task of a
// project is, for the coin rules. Milestones, human gates and waits are not
// work (work=false); the other nodes pay by their complexity.
func (s *Service) RewardInfo(ctx context.Context, org string, t domain.Task) (complexity string, work bool, projectID string) {
	id := nodeIDOf(t)
	if id == "" {
		return "", true, ""
	}
	if lp := s.lookupReq(org, t.RequestID); lp != nil {
		lp.mu.Lock()
		def, ok := lp.nodes[id]
		lp.mu.Unlock()
		if ok {
			return def.Complexity, !def.human(), ""
		}
	}
	rec, err := s.cfg.Store.ByRequest(ctx, org, t.RequestID)
	if err != nil {
		return "", true, ""
	}
	for _, n := range rec.Nodes {
		if n.ID == id {
			return n.Complexity, !n.human(), rec.ID
		}
	}
	return "", true, rec.ID
}

// RewardView is the view of a project the coin rules need (rewards.ProjectSummary).
func (s Summary) RewardView() (id, name, status string, tasksDone int) {
	return s.ID, s.Name, s.Status, s.TasksDone
}
