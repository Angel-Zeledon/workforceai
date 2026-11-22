package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// stallRT plans one hanging task per request text ("stuck <agent>", or "stuck mix": a General task with a dependent of
// Legal) and hangs the first call of each task until its context ends; a retry succeeds.
type stallRT struct {
	fakeRuntime
	mu   sync.Mutex
	seen map[string]bool
}

func (r *stallRT) Plan(_ context.Context, in application.PlanRequest) (application.PlanResponse, error) {
	if in.RequestText == "stuck mix" {
		return application.PlanResponse{Tasks: []application.PlannedTask{
			{Key: "a", Title: "tarea mix", AgentID: "sales"},
			{Key: "b", Title: "después de mix", AgentID: "legal", DependsOn: []string{"a"}},
		}}, nil
	}
	agent := strings.TrimPrefix(in.RequestText, "stuck ")
	return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "tarea " + agent, AgentID: agent}}}, nil
}

func (r *stallRT) RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	r.mu.Lock()
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	first := !r.seen[in.Task.ID]
	r.seen[in.Task.ID] = true
	r.mu.Unlock()
	if first {
		<-ctx.Done()
		return application.RunTaskResponse{}, ctx.Err()
	}
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "hecho", Confidence: 0.9}}, nil
}

// stallEnv is the private-department world (Legal private with ana; beto an admin outside it) with the watchdog
// mounted, its clock three hours ahead.
type stallEnv struct {
	*privateWork
	orch *application.Orchestrator
}

func newStallEnv(t *testing.T) *stallEnv {
	t.Helper()
	se := &stallEnv{}
	se.privateWork = newPrivateWork(t, func(o *opts) {
		o.runtime = &stallRT{}
		prev := o.deps
		o.deps = func(d *api.Deps) {
			if prev != nil {
				prev(d)
			}
			is := &application.InitiativeService{Store: d.Store.(*orgSpy).Store.(application.InitiativeStore), Submit: d.Orch, Cfg: d.Cfg}
			d.Initiative = is
			d.Watchdog = &application.Watchdog{Orch: d.Orch, Initiative: is, Cfg: d.Cfg, Now: func() time.Time { return time.Now().Add(3 * time.Hour) }}
			d.Watchdog.Install()
			se.orch = d.Orch
		}
	})
	return se
}

// stuck submits a request whose first task hangs and returns its task ids by title once that task runs.
func (se *stallEnv) stuck(text string) map[string]string {
	t := se.e.t
	t.Helper()
	ctx := application.WithOrg(context.Background(), se.owner.OrgID)
	reqID, err := se.orch.Submit(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ts, _ := se.e.store.ListTasksByRequest(ctx, se.owner.OrgID, reqID)
		running := false
		for _, x := range ts {
			ids[x.Title] = x.ID
			running = running || x.Status == domain.TaskRunning
		}
		if running {
			return ids
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never ran", text)
	return nil
}

func stalledIDs(t *testing.T, raw string) []string {
	t.Helper()
	var v []application.StalledTask
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range v {
		out = append(out, x.TaskID)
	}
	return out
}

func TestWatchdogAPIFollowsPrivateDepartments(t *testing.T) {
	se := newStallEnv(t)
	org := se.owner.OrgID
	legal := se.stuck("stuck legal")["tarea legal"]
	sales := se.stuck("stuck sales")["tarea sales"]

	if ids := stalledIDs(t, se.body("GET", "/stalled-work", se.ana, 200)); !has(ids, legal) || !has(ids, sales) {
		t.Fatalf("ana (member) = %v", ids)
	}
	if ids := stalledIDs(t, se.body("GET", "/stalled-work", se.beto, 200)); has(ids, legal) || !has(ids, sales) {
		t.Fatalf("beto (admin outside Legal) = %v", ids)
	}
	// hidden like a missing task, for every route
	se.body("GET", "/tasks/"+legal+"/stall", se.beto, 404)
	for _, a := range []string{"retry", "reassign", "cancel"} {
		se.body("POST", "/tasks/"+legal+"/stall/"+a, se.beto, 404)
	}
	got := se.e.json("GET", api1+"/tasks/"+legal+"/stall", se.ana, nil, 200)
	if got["stalled"] != true {
		t.Fatalf("detail = %v", got)
	}
	if st := got["stall"].(map[string]any); st["kind"] != application.StallNoProgress || st["minutes"].(float64) < 170 {
		t.Fatalf("stall = %v", st)
	}
	// the member retries: the same execution runs it again and it finishes
	res := se.e.json("POST", api1+"/tasks/"+legal+"/stall/retry", se.ana, nil, 202)
	if res["status"] != "applied" || res["action"] != "retry" {
		t.Fatalf("retry = %v", res)
	}
	waitStatus(t, se.e, org, legal, domain.TaskDone)
	se.body("POST", "/tasks/"+legal+"/stall/retry", se.ana, 409) // nothing left to retry
}

func TestWatchdogAPICancelConfirmsTheCascadeAndRefusesHiddenWork(t *testing.T) {
	se := newStallEnv(t)
	org := se.owner.OrgID
	ids := se.stuck("stuck mix") // a General task with a dependent of private Legal
	a, b := ids["tarea mix"], ids["después de mix"]

	d := se.e.json("GET", api1+"/tasks/"+a+"/stall", se.beto, nil, 200)
	if d["hidden_cascade"].(float64) != 1 || len(d["cancel_cascade"].([]any)) != 0 {
		t.Fatalf("beto's preview = %v", d)
	}
	resp, raw := se.e.do("POST", api1+"/tasks/"+a+"/stall/cancel", se.beto, map[string]any{"confirm_task_ids": []string{}}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "cascade_hidden") {
		t.Fatalf("beto cancels hidden work = %d %s", resp.StatusCode, raw)
	}
	d = se.e.json("GET", api1+"/tasks/"+a+"/stall", se.ana, nil, 200)
	if c := d["cancel_cascade"].([]any); len(c) != 1 || c[0].(map[string]any)["id"] != b {
		t.Fatalf("ana's preview = %v", d)
	}
	resp, raw = se.e.do("POST", api1+"/tasks/"+a+"/stall/cancel", se.ana, map[string]any{"confirm_task_ids": []string{}}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "cascade_changed") || !strings.Contains(string(raw), b) {
		t.Fatalf("unconfirmed cascade = %d %s", resp.StatusCode, raw)
	}
	res := se.e.json("POST", api1+"/tasks/"+a+"/stall/cancel", se.ana, map[string]any{"confirm_task_ids": []string{b}}, 202)
	if c := res["cancelled"].([]any); len(c) != 1 {
		t.Fatalf("cancel = %v", res)
	}
	waitStatus(t, se.e, org, a, domain.TaskBlocked)
	waitStatus(t, se.e, org, b, domain.TaskBlocked)
}

func TestWatchdogAPIPermissionsAndSettings(t *testing.T) {
	se := newStallEnv(t)
	org := se.owner.OrgID
	task := se.stuck("stuck sales")["tarea sales"]
	mem := se.e.member(se.owner, "member-wd@example.com", auth.RoleMember)
	// a member sees it and may retry (requests:create) but neither reassign nor cancel (tasks:manage)
	if ids := stalledIDs(t, se.body("GET", "/stalled-work", mem, 200)); !has(ids, task) {
		t.Fatalf("member = %v", ids)
	}
	se.body("POST", "/tasks/"+task+"/stall/reassign", mem, 403)
	se.body("POST", "/tasks/"+task+"/stall/cancel", mem, 403)
	se.body("POST", "/stalled-work/scan", mem, 403)
	resp, raw := se.e.do("POST", api1+"/tasks/"+task+"/stall/reassign", se.beto, map[string]any{"agent_id": "legal"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("reassign to an agent whose work beto does not see = %d %s", resp.StatusCode, raw)
	}
	scan := se.e.json("POST", api1+"/stalled-work/scan", se.owner.AccessToken, nil, 200)
	if scan["raised"].(float64) != 1 {
		t.Fatalf("scan = %v", scan)
	}
	se.e.json("POST", api1+"/tasks/"+task+"/stall/retry", mem, nil, 202)
	waitStatus(t, se.e, org, task, domain.TaskDone)

	// the thresholds live in the initiative settings (owner/admin), validated
	sig := application.DefaultSignalSettings()
	sig.StalledWork.TaskMinutes = 2
	resp, _ = se.e.do("PUT", api1+"/settings/initiative", se.owner.AccessToken, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &sig}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("out of range = %d", resp.StatusCode)
	}
	s := se.e.json("GET", api1+"/settings/initiative", se.owner.AccessToken, nil, 200)
	if sw := s["signals"].(map[string]any)["stalled_work"].(map[string]any); sw["task_minutes"].(float64) != 30 || sw["project_minutes"].(float64) != 120 {
		t.Fatalf("defaults = %v", sw)
	}
}

func waitStatus(t *testing.T, e *env, org, id string, want domain.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tk, err := e.store.GetTask(context.Background(), org, id); err == nil && tk.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := e.store.GetTask(context.Background(), org, id)
	t.Fatalf("task %s = %s, want %s", id, tk.Status, want)
}
