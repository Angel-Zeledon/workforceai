package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// The assistant (a General agent) is working on a private office's task. In the listing GET /agents and in the /status
// command, who is not in the office (an admin who is not a member included) sees the agent busy ("working") with no
// activity text and no task id; a member of the office sees what it is doing.
func TestOfficeLeakAgentStateListAndStatusAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte, org := officesWorldSealed(t)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	ctx, now, st := context.Background(), time.Now().UTC(), e.store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateRequest(ctx, org, domain.Request{ID: "req-S", Text: "pedido", Status: domain.RequestRunning, CreatedAt: now, OfficeID: soporte}))
	must(st.CreateTask(ctx, org, domain.Task{ID: "task-S", RequestID: "req-S", Title: "tarea", AgentID: "assistant", OfficeID: soporte,
		Status: domain.TaskRunning, DependsOn: []string{}, CreatedAt: now}))
	taskID := "task-S"
	must(st.UpdateAgentState(ctx, org, "assistant", domain.StateWorking, "SECRETO-redactando el informe", &taskID, 50))

	status := map[string]any{"command": "status", "context": map[string]any{"surface": "office"}}
	read := func(token string) (agents, cmd string) {
		t.Helper()
		resp, raw := e.do("GET", api1+"/agents", token, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("GET /agents = %d", resp.StatusCode)
		}
		resp2, raw2 := e.do("POST", api1+"/commands/run", token, status, nil)
		if resp2.StatusCode != 200 {
			t.Fatalf("/status = %d: %s", resp2.StatusCode, raw2)
		}
		return string(raw), string(raw2)
	}
	for name, tok := range map[string]string{"admin not in the office": owner, "member of another office": ana, "nobody's": luis} {
		agents, cmd := read(tok)
		for where, body := range map[string]string{"GET /agents": agents, "/status": cmd} {
			if strings.Contains(body, "SECRETO") || strings.Contains(body, "task-S") {
				t.Fatalf("%s: %s reveals the private office's activity or task: %.600s", name, where, body)
			}
			if !strings.Contains(body, "working") { // the agent is still shown, busy
				t.Fatalf("%s: %s does not show the assistant busy: %.600s", name, where, body)
			}
		}
	}
	agents, cmd := read(marta)
	if !strings.Contains(agents, "SECRETO-redactando") || !strings.Contains(cmd, "SECRETO-redactando") {
		t.Fatalf("the member does not see what the agent does: agents=%.300s status=%.300s", agents, cmd)
	}
}
