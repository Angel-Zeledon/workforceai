package application_test

import (
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func auditTask(id string, status domain.TaskStatus, metrics map[string]any, findings ...string) domain.Task {
	return domain.Task{ID: id, Title: "Audit " + id, AgentID: "internal_auditor", Status: status,
		Output: &domain.StructuredOutput{Summary: "s", Findings: findings, Metrics: metrics, Evidence: []string{"task:a.metrics.x=1", "task:b.metrics.x=1"}, Confidence: 0.9}}
}

func TestTaskAuditOfOnlyFinishedAudits(t *testing.T) {
	if a := application.TaskAuditOf(domain.Task{ID: "x", Status: domain.TaskDone}); a != nil {
		t.Fatalf("no output = %+v", a)
	}
	if a := application.TaskAuditOf(auditTask("x", domain.TaskDone, map[string]any{"total": 3})); a != nil {
		t.Fatalf("not an audit = %+v", a)
	}
	if a := application.TaskAuditOf(auditTask("x", domain.TaskRunning, map[string]any{"audit_status": "verified"})); a != nil {
		t.Fatalf("unfinished audit = %+v", a)
	}
	a := application.TaskAuditOf(auditTask("x", domain.TaskDone, map[string]any{"audit_status": "Inconsistency", "audit_compared": float64(4), "audit_inconsistencies": 1}, "total: 10 vs 12", " "))
	if a == nil || a.Status != application.AuditInconsistency || a.Compared != 4 || a.Inconsistencies != 1 || len(a.Findings) != 1 || len(a.Evidence) != 2 {
		t.Fatalf("stamp = %+v", a)
	}
	// An unknown status never reads as verified.
	if a := application.TaskAuditOf(auditTask("x", domain.TaskDone, map[string]any{"audit_status": "looks fine"})); a == nil || a.Status != application.AuditUnverified {
		t.Fatalf("unknown status = %+v", a)
	}
}

func TestAuditOfTasksAggregate(t *testing.T) {
	v := func(id string) domain.Task {
		return auditTask(id, domain.TaskDone, map[string]any{"audit_status": "verified"})
	}
	u := auditTask("u", domain.TaskDone, map[string]any{"audit_status": "unverified"})
	bad := auditTask("i", domain.TaskDone, map[string]any{"audit_status": "inconsistency"}, "net income differs")
	plain := domain.Task{ID: "p", Status: domain.TaskDone, Output: &domain.StructuredOutput{Metrics: map[string]any{}}}
	for name, tc := range map[string]struct {
		tasks []domain.Task
		want  string
		n     int
	}{
		"none":         {[]domain.Task{plain}, application.AuditNone, 0},
		"all verified": {[]domain.Task{v("a"), v("b"), plain}, application.AuditVerified, 2},
		"one unverif.": {[]domain.Task{v("a"), u}, application.AuditUnverified, 2},
		"inconsistent": {[]domain.Task{v("a"), u, bad}, application.AuditInconsistency, 3},
	} {
		got := application.AuditOfTasks("req-1", tc.tasks)
		if got.Status != tc.want || len(got.Audits) != tc.n || got.RequestID != "req-1" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
