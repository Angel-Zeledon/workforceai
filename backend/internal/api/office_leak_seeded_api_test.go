package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// The same rule with work written straight into the store, so every text is a marker: a request asked from a private
// office, done by the assistant (a General agent), with its task, its report, its approval, its conversation (its activity line is the recorder's: event_audience tests)
// activity line. Who is not in the office (an admin included) finds none of it in any listing or in the slash commands;
// the member of the office finds it, and so does everybody the work of General.
func TestOfficeLeakSeededWorkAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte, org := officesWorldSealed(t)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	ctx, now, st := context.Background(), time.Now().UTC(), e.store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range []struct{ tag, office string }{{"SECRETO", soporte}, {"ABIERTO", ""}} {
		req := "req-" + w.tag
		must(st.CreateRequest(ctx, org, domain.Request{ID: req, Text: w.tag + "-pedido", Status: domain.RequestDone, CreatedAt: now, OfficeID: w.office}))
		must(st.CreateTask(ctx, org, domain.Task{ID: "task-" + w.tag, RequestID: req, Title: w.tag + "-tarea", AgentID: "assistant", OfficeID: w.office,
			Status: domain.TaskDone, DependsOn: []string{}, CreatedAt: now, FinishedAt: &now, Output: &domain.StructuredOutput{Summary: w.tag + "-resumen"}}))
		must(st.CreateTask(ctx, org, domain.Task{ID: "open-" + w.tag, RequestID: req, Title: w.tag + "-abierta", AgentID: "assistant", OfficeID: w.office,
			Status: domain.TaskRunning, DependsOn: []string{}, CreatedAt: now}))
		must(st.CreateReport(ctx, org, domain.Report{ID: "rep-" + w.tag, RequestID: req, Title: w.tag + "-informe", Summary: w.tag + "-resumen-informe",
			Sections: []domain.Section{}, Contributors: []string{"assistant"}, CreatedAt: now}))
		must(st.CreateConversation(ctx, org, domain.Conversation{ID: "conv-" + w.tag, Title: w.tag + "-conversacion",
			Participants: []string{"user", "assistant"}, RequestID: &req, LastMessageAt: now}))
		must(st.AddMessage(ctx, org, domain.Message{ID: "msg-" + w.tag, ConversationID: "conv-" + w.tag, From: "assistant", To: "user", Kind: domain.MsgChat,
			Text: w.tag + "-mensaje", TS: now, OfficeID: w.office}))
		if _, _, err := e.appr.Request(ctx, domain.Approval{TaskID: "task-" + w.tag, AgentID: "assistant", Action: "email.send", Risk: "high", Title: w.tag + "-aprobacion"}); err != nil {
			t.Fatal(err)
		}
	}

	// the assistant is working on the private office's task: its live state says what, to who sees the office only
	secretTask := "task-SECRETO"
	must(st.UpdateAgentState(ctx, org, "assistant", domain.StateWorking, "SECRETO-redactando el informe", &secretTask, 50))

	paths := []string{"/agents", "/agents/assistant", "/agents/assistant/detail", "/requests", "/requests/req-SECRETO", "/requests/req-SECRETO/audit", "/tasks", "/tasks/task-SECRETO", "/tasks?status=running",
		"/reports", "/reports/rep-SECRETO", "/approvals", "/approvals?status=pending", "/approvals/inbox", "/needs-you", "/away-summary?min_hours=0",
		"/conversations", "/conversations/conv-SECRETO/messages", "/chats/threads", "/artifacts", "/questions", "/projects", "/debates"}
	commands := []map[string]any{
		{"command": "search", "args": map[string]any{"query": "SECRETO"}, "context": map[string]any{"surface": "office"}},
		{"command": "daily-summary", "context": map[string]any{"surface": "office"}},
		{"command": "pending", "context": map[string]any{"surface": "office"}},
		{"command": "status", "context": map[string]any{"surface": "office"}},
	}
	leaks := func(who, token string) {
		t.Helper()
		for _, p := range paths {
			resp, raw := e.do("GET", api1+p, token, nil, nil)
			if resp.StatusCode == 200 && strings.Contains(string(raw), "SECRETO") {
				t.Fatalf("%s: GET %s shows the private office's work: %.500s", who, p, raw)
			}
		}
		for _, c := range commands {
			resp, raw := e.do("POST", api1+"/commands/run", token, c, nil)
			if resp.StatusCode == 200 && strings.Contains(string(raw), "SECRETO") {
				t.Fatalf("%s: /%s shows the private office's work: %.500s", who, c["command"], raw)
			}
		}
	}
	leaks("an admin outside the office", owner)
	leaks("a member of another office", ana)
	leaks("nobody's", luis)

	// the member of the office reads it (so the checks above are not empty), and everybody reads General's work
	for path, want := range map[string]string{"/requests": "SECRETO-pedido", "/tasks": "SECRETO-tarea", "/reports": "SECRETO-informe",
		"/approvals": "SECRETO-aprobacion", "/agents/assistant": "SECRETO-redactando", "/requests/req-SECRETO": "SECRETO-pedido", "/tasks/task-SECRETO": "SECRETO-tarea"} {
		if resp, raw := e.do("GET", api1+path, marta, nil, nil); resp.StatusCode != 200 || !strings.Contains(string(raw), want) {
			t.Fatalf("the member: GET %s = %d, lacks %s: %.300s", path, resp.StatusCode, want, raw)
		}
	}
	// to the rest the agent is busy: its state stays, what it does and the task do not
	for _, tok := range []string{owner, ana, luis} {
		_, raw := e.do("GET", api1+"/agents/assistant", tok, nil, nil)
		if !strings.Contains(string(raw), `"state":"working"`) || strings.Contains(string(raw), "task-SECRETO") {
			t.Fatalf("the assistant is not just busy for the rest: %s", raw)
		}
	}
	for path, want := range map[string]string{"/requests": "ABIERTO-pedido", "/tasks": "ABIERTO-tarea", "/reports": "ABIERTO-informe", "/approvals": "ABIERTO-aprobacion"} {
		for _, tok := range []string{owner, ana, luis} {
			if resp, raw := e.do("GET", api1+path, tok, nil, nil); resp.StatusCode != 200 || !strings.Contains(string(raw), want) {
				t.Fatalf("General's work: GET %s = %d, lacks %s: %.300s", path, resp.StatusCode, want, raw)
			}
		}
	}
	for _, c := range commands[1:3] {
		if _, raw := e.do("POST", api1+"/commands/run", marta, c, nil); !strings.Contains(string(raw), "SECRETO") {
			t.Fatalf("the member: /%s lacks the office's work: %.400s", c["command"], raw)
		}
	}
}
