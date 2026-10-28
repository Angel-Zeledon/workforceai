package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// B15: the report is an estimate with its methodology; money only with the org's own rate.
func TestROIReportAndSettings(t *testing.T) {
	e := newEnv(t, opts{roi: true})
	ctx, org := context.Background(), application.DefaultConfig().OrgID
	now := time.Now().UTC().Add(-time.Hour)
	for _, id := range []string{"r1", "r2"} {
		if err := e.store.CreateTask(ctx, org, domain.Task{ID: id, RequestID: "req", Title: id, AgentID: "accounting", Status: domain.TaskDone, CreatedAt: now, FinishedAt: &now,
			Output: &domain.StructuredOutput{Metrics: map[string]any{}}}); err != nil {
			t.Fatal(err)
		}
	}
	resp, body := e.do("GET", "/api/v1/roi", "", nil, nil)
	var rep application.ROIReport
	_ = json.Unmarshal(body, &rep)
	if resp.StatusCode != 200 || rep.Methodology.Kind != "estimate" || rep.Methodology.RateSource != "unset" || rep.Methodology.DefaultMinutesSource != "assumption" ||
		rep.Org.TasksCounted != 2 || rep.Org.EstMinutes != 60 || rep.Org.EstHumanCostUSD != nil {
		t.Fatalf("report = %d %s", resp.StatusCode, body)
	}

	resp, body = e.do("GET", "/api/v1/settings/roi", "", nil, nil)
	var view struct {
		application.ROISettings
		Saved bool `json:"saved"`
	}
	_ = json.Unmarshal(body, &view)
	if resp.StatusCode != 200 || view.Saved || view.HourlyRateUSD != nil || view.DefaultMinutes != application.DefaultROIMinutes {
		t.Fatalf("settings = %d %s", resp.StatusCode, body)
	}
	rate := 50.0
	resp, body = e.do("PUT", "/api/v1/settings/roi", "", application.ROISettings{HourlyRateUSD: &rate, DefaultMinutes: 30, RoleMinutes: map[string]int{"accounting": 45}}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("put = %d %s", resp.StatusCode, body)
	}
	if c := e.status("PUT", "/api/v1/settings/roi", "", map[string]any{"default_minutes": 9999}); c != 400 {
		t.Errorf("invalid put = %d", c)
	}
	_, body = e.do("GET", "/api/v1/roi?window_days=7", "", nil, nil)
	_ = json.Unmarshal(body, &rep)
	if rep.Methodology.WindowDays != 7 || rep.Methodology.RateSource != "org" || rep.Org.EstMinutes != 90 || rep.Org.EstHumanCostUSD == nil || *rep.Org.EstHumanCostUSD != 75 {
		t.Fatalf("report with rate = %s", body)
	}
	// Audited.
	page, _ := e.store.QueryAudit(ctx, org, domain.AuditQuery{Action: "roi.settings_changed"})
	if len(page.Items) == 0 {
		t.Error("roi.settings_changed not audited")
	}
}

func TestROIPermissions(t *testing.T) {
	e := newEnv(t, opts{auth: true, roi: true})
	owner := e.register("owner@example.com", "Acme")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	if c := e.status("GET", "/api/v1/roi", "", nil); c != 401 {
		t.Errorf("anonymous = %d", c)
	}
	if c := e.status("GET", "/api/v1/roi", viewer, nil); c != 200 {
		t.Errorf("viewer read = %d", c)
	}
	if c := e.status("PUT", "/api/v1/settings/roi", member, application.ROISettings{DefaultMinutes: 10}); c != 403 {
		t.Errorf("member put = %d, want 403", c)
	}
	if c := e.status("PUT", "/api/v1/settings/roi", owner.AccessToken, application.ROISettings{DefaultMinutes: 10}); c != 200 {
		t.Errorf("owner put = %d", c)
	}
}
