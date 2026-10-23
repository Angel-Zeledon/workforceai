package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// B7: the internal auditor's stamp of a request is served as declared by its tasks.
func TestRequestAuditStamp(t *testing.T) {
	e := newEnv(t, opts{})
	ctx, org := context.Background(), application.DefaultConfig().OrgID
	now := time.Now().UTC()
	if err := e.store.CreateRequest(ctx, org, domain.Request{ID: "req-audit", Text: "cierre", Status: domain.RequestDone, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateRequest(ctx, org, domain.Request{ID: "req-plain", Text: "otro", Status: domain.RequestDone, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, req, status string, findings ...string) {
		out := &domain.StructuredOutput{Summary: id, Findings: findings, Metrics: map[string]any{}, Evidence: []string{"task:a.metrics.net_income=10", "task:b.metrics.net_income=12"}}
		if status != "" {
			out.Metrics["audit_status"] = status
		}
		if err := e.store.CreateTask(ctx, org, domain.Task{ID: id, RequestID: req, Title: id, AgentID: "analyst", Status: domain.TaskDone, CreatedAt: now, FinishedAt: &now, Output: out}); err != nil {
			t.Fatal(err)
		}
	}
	mk("t-work", "req-audit", "")
	mk("t-audit", "req-audit", "inconsistency", "net_income: 10 vs 12")
	mk("t-other", "req-plain", "")

	resp, body := e.do("GET", "/api/v1/requests/req-audit/audit", "", nil, nil)
	var got application.RequestAudit
	_ = json.Unmarshal(body, &got)
	if resp.StatusCode != 200 || got.Status != application.AuditInconsistency || len(got.Audits) != 1 || got.Audits[0].TaskID != "t-audit" || len(got.Audits[0].Findings) != 1 {
		t.Fatalf("audit = %d %s", resp.StatusCode, body)
	}
	resp, body = e.do("GET", "/api/v1/requests/req-plain/audit", "", nil, nil)
	_ = json.Unmarshal(body, &got)
	if resp.StatusCode != 200 || got.Status != application.AuditNone || got.Audits == nil || len(got.Audits) != 0 {
		t.Fatalf("plain = %d %s", resp.StatusCode, body)
	}
	if c := e.status("GET", "/api/v1/requests/missing/audit", "", nil); c != 404 {
		t.Errorf("missing request = %d", c)
	}
	// The task output itself still carries the declared status (pass-through for the dashboard).
	_, body = e.do("GET", "/api/v1/tasks/t-audit", "", nil, nil)
	var tk domain.Task
	_ = json.Unmarshal(body, &tk)
	if tk.Output == nil || tk.Output.Metrics["audit_status"] != "inconsistency" {
		t.Errorf("task output = %s", body)
	}
}
