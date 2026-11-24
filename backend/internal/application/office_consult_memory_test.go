package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// consultRT records what the consulted agent receives in a task consult (the chat consult is recorded by chatRT).
type consultRT struct {
	*chatRT
	mu       sync.Mutex
	consults []string
}

func (c *consultRT) Consult(ctx context.Context, in application.ConsultRequest) (application.ConsultResponse, error) {
	raw, _ := json.Marshal(in)
	c.mu.Lock()
	c.consults = append(c.consults, string(raw))
	c.mu.Unlock()
	return c.chatRT.Consult(ctx, in)
}

// "sales" works for Legal (its base) and for Ventas and keeps different memories in each; "accounting" works for
// Ventas only. Asked from Ventas, accounting consults sales: what sales has learned for Legal does not cross into the
// consult, neither the one between tasks nor the one of the chat, and the consulted agent answers for Ventas only.
func TestIsolation_ConsultedAgentsMemoryDoesNotCrossOffices(t *testing.T) {
	rt := &consultRT{chatRT: newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{Intent: "question", Topic: "finance", Responders: []application.RouteResponder{primary("accounting")},
			Consult: &application.RouteConsult{AgentID: "sales", Reason: "es de ventas"}}, nil
	})}
	rt.plan = func(in application.PlanRequest) (application.PlanResponse, error) {
		agent := "sales"
		if strings.HasPrefix(in.RequestText, "consulta") {
			agent = "accounting"
		}
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Responder", AgentID: agent}}}, nil
	}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho")
		switch {
		case strings.HasPrefix(in.Context.RequestText, "escribe legal"):
			res = okResult("LEGALSECRET propuesta confidencial contrato")
			res.Output.MemoryNotes = []string{"LEGALNOTE propuesta confidencial contrato"}
		case strings.HasPrefix(in.Context.RequestText, "escribe ventas"):
			res = okResult("VENTASSECRET propuesta confidencial contrato")
			res.Output.MemoryNotes = []string{"VENTASNOTE propuesta confidencial contrato"}
		case strings.HasPrefix(in.Context.RequestText, "consulta"):
			res.Consults = []application.ConsultRequestItem{{ToAgentID: "sales", Question: "¿qué sabes de la propuesta confidencial?"}}
		}
		return res, nil
	}
	var chatMu sync.Mutex
	var chatSales []string
	rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		raw, _ := json.Marshal(in)
		if in.Agent.ID == "sales" {
			chatMu.Lock()
			chatSales = append(chatSales, string(raw))
			chatMu.Unlock()
		}
		if in.Consult != nil {
			return application.ChatReplyResponse{Text: "Eso lo veo en ventas.", Kind: "answer"}, nil
		}
		return application.ChatReplyResponse{Text: "Se lo pregunto a Ventas.",
			Consult: &application.ChatReplyConsult{ToAgentID: "sales", Question: "¿qué sabes de la propuesta confidencial?"}}, nil
	}
	h := newChat(t, rt, nil)
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.FTS{Docs: store}})
	scope := scopeFake{}
	svc.SetOfficeScope(scope.officeOf, func(context.Context, string, string) (bool, error) { return false, nil })
	h.orch.SetMemorySource(svc)
	h.orch.SetOfficeScope(scope)

	run := func(office, text string) {
		t.Helper()
		id, err := h.orch.Submit(application.WithRequestOffice(context.Background(), office), text)
		if err != nil {
			t.Fatal(err)
		}
		h.waitFor("done: "+text, func() bool { return h.tasks(id)["Responder"].Status == domain.TaskDone })
	}
	run("o-legal", "escribe legal propuesta confidencial contrato")
	run("o-ven", "escribe ventas propuesta confidencial contrato")
	ctx := application.WithOrg(context.Background(), org)
	h.waitFor("memory written", func() bool { l, _ := svc.List(ctx, org, "sales", 20); return len(l) >= 4 })

	// the consult between tasks, from a Ventas request
	run("o-ven", "consulta ventas propuesta confidencial contrato")
	rt.mu.Lock()
	consults := strings.Join(rt.consults, "\n")
	rt.mu.Unlock()
	if !strings.Contains(consults, "propuesta confidencial") {
		t.Fatalf("sales was not consulted: %q", consults)
	}
	if strings.Contains(consults, "LEGAL") || strings.Contains(consults, "escribe legal") {
		t.Fatalf("the consult carries what sales knows for Legal: %s", consults)
	}

	// the consult of the chat, asked from Ventas
	if _, err := h.orch.PostChat(application.WithRequestOffice(context.Background(), "o-ven"), "agent:accounting", "¿cómo va la propuesta confidencial?"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	chatMu.Lock()
	got := strings.Join(chatSales, "\n")
	chatMu.Unlock()
	if got == "" {
		t.Fatal("sales was not consulted in the chat")
	}
	if strings.Contains(got, "LEGAL") {
		t.Fatalf("the chat consult carries what sales knows for Legal: %s", got)
	}
}
