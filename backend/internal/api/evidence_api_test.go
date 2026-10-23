package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// B6: GET /reports/{id}/evidence lists the checked citations of the report's
// tasks, verified first; an unverified one never carries a source.
func TestReportEvidenceEndpoint(t *testing.T) {
	e := newEnv(t, opts{})
	ctx, org := context.Background(), domain.DemoOrgID
	now := time.Now().UTC()
	if err := e.store.CreateRequest(ctx, org, domain.Request{ID: "rq-ev", Text: "cierre", Status: domain.RequestDone, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	out := &domain.StructuredOutput{Summary: "Margen 24%", Citations: []domain.Citation{
		{Value: "$9", Ref: "task:nope", Kind: "task", Status: domain.CitationUnverified, Reason: application.ReasonNotSeen,
			Source: &domain.CitationSource{TaskID: "leak"}}, // a stale source on an unverified row must not reach the UI
		{Value: "24%", Ref: "task:t-a", Kind: "task", Status: domain.CitationVerified, ValueChecked: true,
			Source: &domain.CitationSource{TaskID: "t-a", Title: "Margen", AgentID: "accounting"}},
	}}
	out.Normalize()
	if err := e.store.CreateTask(ctx, org, domain.Task{ID: "t-b", RequestID: "rq-ev", Title: "Informe", AgentID: "analyst", Status: domain.TaskDone, Output: out, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateReport(ctx, org, domain.Report{ID: "rep-ev", RequestID: "rq-ev", Title: "Informe", Summary: "Margen 24%", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	resp, body := e.do("GET", "/api/v1/reports/rep-ev/evidence", "", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	var ev application.ReportEvidence
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Total != 2 || ev.Verified != 1 || ev.Citations[0].Value != "24%" || ev.Citations[0].TaskID != "t-b" || ev.Citations[0].Source.TaskID != "t-a" {
		t.Fatalf("evidence: %s", body)
	}
	if ev.Citations[1].Source != nil {
		t.Fatalf("an unverified citation must not carry a source: %s", body)
	}
	if c := e.status("GET", "/api/v1/reports/missing/evidence", "", nil); c != 404 {
		t.Fatalf("unknown report = %d", c)
	}
}
