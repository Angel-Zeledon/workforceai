package projects_test

import (
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// B7: a finished task whose output declares an audit status carries the stamp on its node; other nodes do not.
func TestAuditStampOnTheNode(t *testing.T) {
	rt := newRT()
	e := newEnv(t, rt, nil, nil)
	id := e.draft(tFC, budgetBig)
	var target projects.Node
	for _, n := range nodesOf(e.detail(id), projects.KindTask) {
		if n.AgentID != nil && n.ApprovalAction == "" {
			target = n
			break
		}
	}
	rt.shape = func(title string, out *domain.StructuredOutput) {
		if title == target.Title {
			out.Metrics = map[string]any{"audit_status": "inconsistency", "audit_compared": 2, "audit_inconsistencies": 1}
			out.Findings = []string{"net_income: 10 vs 12"}
		}
	}
	e.launch(id, budgetBig)
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == "done" })
	d := e.detail(id)
	n := nodeByID(d, target.ID)
	if n.Audit == nil || n.Audit.Status != application.AuditInconsistency || len(n.Audit.Findings) != 1 || n.Audit.Inconsistencies != 1 {
		t.Fatalf("node audit = %+v", n.Audit)
	}
	for _, o := range d.Nodes {
		if o.ID != target.ID && o.Audit != nil {
			t.Fatalf("node %q has an audit stamp it never declared", o.Title)
		}
	}
}
