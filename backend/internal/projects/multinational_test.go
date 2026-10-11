package projects_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// Multinational-scale projects run end to end in simulation with only the demo
// office: every per-country / per-entity block in parallel, the group work, a
// human gate, and every task done. Sizes are the maximum each template allows.

func (e *cashEnv) runTemplate(t *testing.T, key string, params map[string]string, parallel int) (projects.Detail, int, time.Duration) {
	t.Helper()
	r, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: key, Params: params, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Get(e.ctx, r.ID)
	total := leafCount(d)
	start := time.Now()
	if _, err := e.svc.Launch(e.ctx, r.ID, projects.LaunchBody{ApprovedBudgetUSD: 500, AcknowledgeUnderbudget: true, MaxParallel: parallel}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		all, _ := e.st.ListApprovals(e.ctx, e.org, "")
		for _, a := range all {
			if a.Status == domain.ApprovalPending {
				_, _ = e.appr.Decide(e.ctx, a.ID, "approve", "reviewed by the CFO / board / IC")
			}
		}
		dd, _ := e.svc.Get(e.ctx, r.ID)
		if dd.Project.Status == "done" {
			return dd, total, time.Since(start)
		}
		time.Sleep(10 * time.Millisecond)
	}
	dd, _ := e.svc.Get(e.ctx, r.ID)
	t.Fatalf("%s timeout: %d/%d done, status %s", key, dd.Project.TasksDone, total, dd.Project.Status)
	return dd, total, 0
}

func TestMultinationalProjectsAtMaximumSize(t *testing.T) {
	for _, tc := range []struct {
		key      string
		params   map[string]string
		tasks    int
		gateWord string
		mention  []string // countries/entities that must appear in task titles
	}{
		{"group_consolidated_close", map[string]string{"subsidiaries": "20"}, 20*4 + 6 + 1, "CFO", []string{"México", "Alemania", "República Dominicana"}},
		{"market_expansion", map[string]string{"countries": "12"}, 12*7 + 3 + 1, "directorio", []string{"Colombia", "Uruguay"}},
		{"acquisition_due_diligence", map[string]string{"entities": "10"}, 10*9 + 4 + 1, "comité", []string{"Entidad 10"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			e := newCashEnv(t, false) // the demo office only: nobody is hired
			// Until more than 8 calls overlapped once, hold each call briefly so the peak measures
			// what the scheduler allows, not goroutine timing in a loaded -race run.
			e.rt.onRun = func(application.RunTaskRequest) {
				for deadline := time.Now().Add(60 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
					e.rt.mu.Lock()
					p := e.rt.peak
					e.rt.mu.Unlock()
					if p > 8 {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			dd, total, took := e.runTemplate(t, tc.key, tc.params, 24)
			if total != tc.tasks {
				t.Fatalf("%d tasks, want %d", total, tc.tasks)
			}
			if dd.Project.TasksDone != dd.Project.TasksTotal || dd.Project.Failed != 0 {
				t.Fatalf("all tasks must be done: %d/%d failed %d", dd.Project.TasksDone, dd.Project.TasksTotal, dd.Project.Failed)
			}
			if e.rt.peak <= 8 {
				t.Fatalf("peak parallelism %d: the per-country blocks must run in parallel", e.rt.peak)
			}
			titles := ""
			agents := map[string]bool{}
			gates := 0
			for _, n := range dd.Nodes {
				if n.Kind == projects.KindGroup {
					continue
				}
				if n.State != "done" {
					t.Fatalf("node %q is %s", n.Title, n.State)
				}
				titles += n.Title + "\n"
				if n.AgentID != nil {
					agents[*n.AgentID] = true
				}
				if n.Kind == projects.KindGate {
					gates++
					if !strings.Contains(n.Title, tc.gateWord) {
						t.Fatalf("gate title %q should mention %q", n.Title, tc.gateWord)
					}
				}
			}
			if gates != 1 {
				t.Fatalf("exactly one human gate expected, got %d", gates)
			}
			for _, w := range tc.mention {
				if !strings.Contains(titles, w) {
					t.Fatalf("titles must name %q", w)
				}
			}
			if len(agents) < 3 {
				t.Fatalf("a multinational project involves several roles, got %v", agents)
			}
			t.Log(fmt.Sprintf("%s: %d tasks, peak %d, %d roles, %s", tc.key, total, e.rt.peak, len(agents), took.Round(time.Millisecond)))
		})
	}
}
