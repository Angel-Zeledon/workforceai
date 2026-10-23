package away_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

const org = "org-a"

var (
	left = time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC) // the person closed the laptop
	back = left.Add(14 * time.Hour)                       // and came back next morning
)

type env struct {
	st    *memory.Store
	svc   *away.Service
	now   *time.Time
	marks *away.MemMarks
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := memory.New()
	ctx := context.Background()
	agents := []domain.Agent{{ID: "ana", Name: "Ana", Role: "sales", State: domain.StateIdle}, {ID: "bob", Name: "Bob", Role: "analyst", State: domain.StateWorking}}
	if err := st.Seed(ctx, domain.Organization{ID: org, Name: "A", Slug: "a"}, agents); err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, marks: away.NewMemMarks()}
	now := left
	e.now = &now
	e.svc = away.New(away.Config{Marks: e.marks, History: st, OrgID: org, Now: func() time.Time { return *e.now }})
	return e
}

func (e *env) ctx() context.Context { return application.WithOrg(context.Background(), org) }

func (e *env) event(t *testing.T, typ, agent string, at time.Time, payload any) {
	t.Helper()
	// Round trip through JSON like the Postgres store does, so the test exercises the same decoding.
	b, _ := json.Marshal(payload)
	if err := e.st.SaveEvent(context.Background(), org, domain.Event{ID: fmt.Sprintf("ev-%d", at.UnixNano()), Type: typ, TS: at, OrgID: org, AgentID: agent, Payload: json.RawMessage(b)}); err != nil {
		t.Fatal(err)
	}
}

var owner = away.Viewer{UserID: "u-owner", Role: "owner", CanDecide: true, ReadApprovals: true}

// seed builds a believable night: work done, a failure, a blocked task, approvals, a suggestion, an alert, spend.
func (e *env) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	at := func(h float64) time.Time { return left.Add(time.Duration(h * float64(time.Hour))) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.st.CreateRequest(ctx, org, domain.Request{ID: "r1", Text: "Prepara el reporte de ventas de octubre", Status: domain.RequestDone, CreatedAt: at(1)}))
	must(e.st.CreateRequest(ctx, org, domain.Request{ID: "r2", Text: "Concilia las facturas del proveedor", Status: domain.RequestFailed, CreatedAt: at(2)}))
	e.event(t, domain.EvRequestCompleted, "", at(3), map[string]any{"request_id": "r1", "report_id": "rep1"})
	for i := 0; i < 4; i++ {
		e.event(t, domain.EvTaskCompleted, "ana", at(1.5+float64(i)*0.1), map[string]any{"task": domain.Task{ID: fmt.Sprintf("t%d", i), AgentID: "ana", Title: "x"}})
	}
	e.event(t, domain.EvTaskFailed, "bob", at(4), map[string]any{"task": domain.Task{ID: "t9", AgentID: "bob", Title: "Descargar facturas"}, "error": "runtime timeout"})
	e.event(t, domain.EvTaskBlocked, "bob", at(5), map[string]any{"task": domain.Task{ID: "t10", AgentID: "bob", Title: "Enviar correo"}, "reason": "dependencies"})
	e.event(t, domain.EvTaskBlocked, "bob", at(6), map[string]any{"task": domain.Task{ID: "t10", AgentID: "bob", Title: "Enviar correo"}, "reason": "dependencies"})
	e.event(t, domain.EvApprovalResolved, "ana", at(7), map[string]any{"approval": domain.Approval{ID: "ap-old", Title: "Publicar post", Status: domain.ApprovalRejected, Note: "sin respuesta: tiempo de espera agotado"}})
	e.event(t, domain.EvInitiativeRaised, "ana", at(8), map[string]any{"initiative_id": "in1", "agent_id": "ana", "signal": "task_failed", "title": "¿Reviso las facturas que fallaron?"})
	e.event(t, "anomaly.detected", "", at(9), map[string]any{"rule": "spend_spike", "explanation": "spend 5x the hourly average"})
	e.event(t, "connection.status_changed", "", at(9.5), map[string]any{"connection_id": "c1", "from": "pending", "to": "active"}) // good news: not an alert
	e.event(t, domain.EvAgentState, "bob", at(1), map[string]any{"agent_id": "bob", "state": "working", "activity": "Descargando"})
	e.event(t, domain.EvAgentState, "bob", at(4), map[string]any{"agent_id": "bob", "state": "error", "activity": "Falló"})
	e.event(t, domain.EvAgentState, "ana", at(2), map[string]any{"agent_id": "ana", "state": "working", "activity": "Redactando"})
	// Outside the window: before leaving and after coming back.
	e.event(t, domain.EvTaskFailed, "bob", left.Add(-time.Hour), map[string]any{"task": domain.Task{ID: "old", Title: "old"}})
	e.event(t, domain.EvTaskFailed, "bob", back.Add(time.Minute), map[string]any{"task": domain.Task{ID: "new", Title: "new"}})
	must(e.st.AddAudit(ctx, org, domain.AuditLog{ID: "au1", TS: at(4.1), Actor: "system", Action: "request.status.failed", Entity: "request", EntityID: "r2"}))
	must(e.st.AddUsage(ctx, org, domain.UsageEntry{ID: "u1", AgentID: "ana", CostUSD: 0.12, TS: at(2)}))
	must(e.st.AddUsage(ctx, org, domain.UsageEntry{ID: "u2", AgentID: "bob", CostUSD: 0.30, TS: at(4)}))
	must(e.st.AddUsage(ctx, org, domain.UsageEntry{ID: "u3", AgentID: "bob", CostUSD: 9.99, TS: left.Add(-2 * time.Hour)})) // before
	// Approvals pending now: one for this person (new, high risk), one the owner requested under no-self-approval,
	// one already approved by this person (double approval), one old low risk.
	mk := func(id, risk string, created time.Time, f func(*domain.Approval)) {
		a := domain.Approval{ID: id, TaskID: "t-" + id, AgentID: "ana", Action: "email.send", Title: "Aprobar " + id, Risk: risk, Status: domain.ApprovalPending, CreatedAt: created, RequiredApprovals: 1}
		if f != nil {
			f(&a)
		}
		must(e.st.CreateApproval(ctx, org, a))
	}
	mk("ap-high", "high", at(10), nil)
	mk("ap-low", "low", left.Add(-48*time.Hour), nil)
	mk("ap-self", "medium", at(11), func(a *domain.Approval) { a.NoSelfApproval, a.RequestedBy = true, "u-owner" })
	mk("ap-mine", "high", at(11), func(a *domain.Approval) {
		a.RequiredApprovals = 2
		a.Decisions = []domain.ApprovalDecision{{By: "u-owner", TS: at(11)}}
	})
	mk("ap-admin", "medium", at(12), func(a *domain.Approval) { a.RequiredRole = "owner" })
}

func TestFirstVisitHasNoSummary(t *testing.T) {
	e := newEnv(t)
	s, err := e.svc.Summary(e.ctx(), owner, -1)
	if err != nil || !s.FirstVisit || s.Show || !s.Quiet || s.ActNow == nil || s.Sections == nil {
		t.Fatalf("first visit = %+v %v", s, err)
	}
}

func TestSummaryOfANight(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Heartbeat(e.ctx(), owner); err != nil { // last seen at "left"
		t.Fatal(err)
	}
	e.seed(t)
	*e.now = back
	s, err := e.svc.Summary(e.ctx(), owner, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Show || s.Quiet || s.Dismissed || s.AwayMinutes != 14*60 || s.MinHours != 8 || !s.Since.Equal(left) || !s.Until.Equal(back) || s.Method != "deterministic" {
		t.Fatalf("header = show %v quiet %v dismissed %v away %d min %v since %v until %v", s.Show, s.Quiet, s.Dismissed, s.AwayMinutes, s.MinHours, s.Since, s.Until)
	}
	c := s.Counts
	if c.RequestsCompleted != 1 || c.RequestsFailed != 1 || c.TasksCompleted != 4 || c.TasksFailed != 1 || c.TasksBlocked != 2 ||
		c.ApprovalsDecided != 1 || c.Initiatives != 1 || c.Alerts != 1 {
		t.Fatalf("counts = %+v", c)
	}
	// Waiting for the owner: ap-high, ap-low, ap-admin (owner role); not ap-self (no self-approval) nor ap-mine (already approved).
	if c.ApprovalsWaiting != 3 || c.ApprovalsWaitingNew != 2 || c.ApprovalsWaitingOther != 2 {
		t.Fatalf("approval counts = %+v", c)
	}
	if len(s.ActNow) != 3 || s.ActNow[0].RefID != "ap-high" || !s.ActNow[0].New || s.ActNow[0].Severity != away.SevHigh ||
		s.ActNow[1].RefID != "ap-admin" || s.ActNow[2].RefID != "ap-low" || s.ActNow[2].New {
		t.Fatalf("act now = %+v", s.ActNow)
	}
	keys := []string{}
	for _, sec := range s.Sections {
		keys = append(keys, sec.Key)
	}
	if fmt.Sprint(keys) != "[alerts problems done suggestions decided]" {
		t.Fatalf("sections = %v", keys)
	}
	alerts, problems, done, sugg, decided := s.Sections[0], s.Sections[1], s.Sections[2], s.Sections[3], s.Sections[4]
	if alerts.Total != 1 || alerts.Items[0].Code != "anomaly:spend_spike" || alerts.Items[0].Severity != away.SevHigh {
		t.Fatalf("alerts = %+v", alerts)
	}
	// Problems: the failed request first (high), then the failed task, then the blocked task folded once.
	if problems.Total != 3 || problems.Items[0].Kind != away.KindRequestFailed || problems.Items[0].Title != "Concilia las facturas del proveedor" ||
		problems.Items[1].Kind != away.KindTaskFailed || problems.Items[1].Detail != "runtime timeout" ||
		problems.Items[2].Kind != away.KindTaskBlocked || problems.Items[2].Repeats != 1 || problems.Items[2].RefID != "t10" {
		t.Fatalf("problems = %+v", problems.Items)
	}
	if done.Items[0].Ref != "report" || done.Items[0].RefID != "rep1" || done.Items[0].Detail != "r1" || done.Items[0].Title != "Prepara el reporte de ventas de octubre" {
		t.Fatalf("done = %+v", done.Items)
	}
	if sugg.Items[0].RefID != "in1" || sugg.Items[0].Code != "task_failed" {
		t.Fatalf("suggestions = %+v", sugg.Items)
	}
	if decided.Items[0].Code != "expired" {
		t.Fatalf("decided = %+v", decided.Items)
	}
	if s.Spend.USD != 0.42 || s.Spend.Calls != 2 || s.Spend.Top[0].AgentID != "bob" {
		t.Fatalf("spend = %+v", s.Spend)
	}
	if len(s.Agents) != 2 || s.Agents[0].AgentID != "ana" || s.Agents[0].TasksCompleted != 4 || s.Agents[1].TasksFailed != 1 || s.Agents[1].Name != "Bob" {
		t.Fatalf("agents = %+v", s.Agents)
	}
	tl := s.Timelapse
	if !tl.Available || tl.Total != 3 || tl.Sampled || len(tl.Frames) != 3 || tl.Frames[0].AgentID != "bob" || tl.Frames[1].AgentID != "ana" || tl.Frames[2].State != "error" {
		t.Fatalf("timelapse = %+v", tl)
	}

	// Asking again after the heartbeat of the new visit gives the same window.
	if _, err := e.svc.Heartbeat(e.ctx(), owner); err != nil {
		t.Fatal(err)
	}
	*e.now = back.Add(5 * time.Minute)
	s2, err := e.svc.Summary(e.ctx(), owner, -1)
	if err != nil || !s2.Show || !s2.Since.Equal(left) || !s2.Until.Equal(back) || s2.Counts != s.Counts {
		t.Fatalf("after heartbeat = %+v %v", s2.Counts, err)
	}
	// Dismissed: still computable, no longer shown on its own.
	if _, err := e.svc.Dismiss(e.ctx(), owner); err != nil {
		t.Fatal(err)
	}
	s3, _ := e.svc.Summary(e.ctx(), owner, -1)
	if s3.Show || !s3.Dismissed || s3.Counts.TasksCompleted != 4 {
		t.Fatalf("dismissed = show %v dismissed %v", s3.Show, s3.Dismissed)
	}
}

func TestThresholdAndViewerPermissions(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Heartbeat(e.ctx(), owner); err != nil {
		t.Fatal(err)
	}
	e.seed(t)
	*e.now = back
	// 14 hours away is under a 24-hour threshold: computed, not shown.
	s, _ := e.svc.Summary(e.ctx(), owner, 24)
	if s.Show || s.MinHours != 24 || s.Quiet {
		t.Fatalf("24h threshold = show %v min %v", s.Show, s.MinHours)
	}
	if s, _ := e.svc.Summary(e.ctx(), owner, 500); s.MinHours != away.MaxMinHours {
		t.Fatalf("clamp = %v", s.MinHours)
	}
	// A member without approvals:decide sees nothing to act on; a viewer without approvals:read sees no approvals at all.
	member := away.Viewer{UserID: "u-m", Role: "member", CanDecide: false, ReadApprovals: true}
	if _, err := e.svc.Heartbeat(e.ctx(), member); err != nil {
		t.Fatal(err)
	}
	// member was first seen now: no absence yet.
	if s, _ := e.svc.Summary(e.ctx(), member, -1); !s.Quiet || s.Show {
		t.Fatalf("member first visit = %+v", s.Counts)
	}
	*e.now = back.Add(10 * time.Hour)
	s, _ = e.svc.Summary(e.ctx(), member, -1)
	if len(s.ActNow) != 0 || s.Counts.ApprovalsWaiting != 0 || s.Counts.ApprovalsWaitingOther != 5 {
		t.Fatalf("member approvals = %+v", s.Counts)
	}
	blind := away.Viewer{UserID: "u-m", Role: "member"}
	s, _ = e.svc.Summary(e.ctx(), blind, -1)
	for _, sec := range s.Sections {
		if sec.Key == away.SectionDecided {
			t.Fatal("decided approvals shown to a viewer without approvals:read")
		}
	}
	if s.Counts.ApprovalsWaitingOther != 0 || s.Counts.ApprovalsDecided != 0 {
		t.Fatalf("blind approvals = %+v", s.Counts)
	}
	if _, err := e.svc.Summary(e.ctx(), away.Viewer{}, -1); err == nil {
		t.Fatal("a summary needs a user")
	}
}

func TestQuietNightAndLongAbsence(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Heartbeat(e.ctx(), owner); err != nil {
		t.Fatal(err)
	}
	*e.now = back
	s, err := e.svc.Summary(e.ctx(), owner, -1)
	if err != nil || !s.Quiet || s.Show || s.Timelapse.Available {
		t.Fatalf("quiet = %+v %v", s, err)
	}
	// Three weeks away: only the last 14 days are summarized, the absence is still reported whole.
	*e.now = left.Add(21 * 24 * time.Hour)
	s, _ = e.svc.Summary(e.ctx(), owner, -1)
	if !s.Clipped || s.AwayMinutes != 21*24*60 || !s.Since.Equal(s.Until.Add(-away.DefaultMaxLookback)) {
		t.Fatalf("clipped = %v %d %v %v", s.Clipped, s.AwayMinutes, s.Since, s.Until)
	}
}

func TestTimelapseSamplingKeepsEveryAgentsLastState(t *testing.T) {
	var states []domain.Event
	for i := 0; i < 1000; i++ {
		agent := "ana"
		if i%10 == 0 {
			agent = "bob"
		}
		st := "working"
		if i == 990 {
			st = "error" // bob's last change
		}
		states = append(states, domain.Event{Type: domain.EvAgentState, AgentID: agent, TS: left.Add(time.Duration(i) * time.Second),
			Payload: map[string]any{"agent_id": agent, "state": st}})
	}
	s := away.Build(away.Facts{States: states, Counts: map[string]int{domain.EvAgentState: 1200}}, owner, left, back, back, 8)
	tl := s.Timelapse
	if !tl.Sampled || tl.Total != 1200 || len(tl.Frames) > away.MaxFrames || len(tl.Frames) < away.MaxFrames-5 {
		t.Fatalf("sampled = %v total %d frames %d", tl.Sampled, tl.Total, len(tl.Frames))
	}
	lastBob := ""
	for i, f := range tl.Frames {
		if i > 0 && f.At.Before(tl.Frames[i-1].At) {
			t.Fatal("frames out of order")
		}
		if f.AgentID == "bob" {
			lastBob = f.State
		}
	}
	if lastBob != "error" {
		t.Fatalf("bob's last frame = %q", lastBob)
	}
}

func TestCanDecideMirrorsTheApprovalRules(t *testing.T) {
	ap := domain.Approval{ID: "a", AgentID: "ana", Status: domain.ApprovalPending, RequiredRole: "admin"}
	cases := []struct {
		name string
		v    away.Viewer
		ap   domain.Approval
		want bool
	}{
		{"owner", owner, ap, true},
		{"member below required role", away.Viewer{UserID: "m", Role: "member", CanDecide: true}, ap, false},
		{"no decide permission", away.Viewer{UserID: "o", Role: "owner"}, ap, false},
		{"unknown role", away.Viewer{UserID: "x", Role: "", CanDecide: true}, domain.Approval{Status: domain.ApprovalPending}, false},
		{"the agent itself", away.Viewer{UserID: "ana", Role: "owner", CanDecide: true}, ap, false},
		{"resolved", owner, domain.Approval{Status: domain.ApprovalApproved}, false},
	}
	for _, c := range cases {
		if got := away.CanDecide(c.ap, c.v); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
