package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ---- fakes ----

// chatRT is a runtime that also speaks the chat layer.
type chatRT struct {
	*fakeRuntime
	route func(application.RouteRequest) (application.RouteResponse, error)
	reply func(application.ChatReplyRequest) (application.ChatReplyResponse, error)

	mu      sync.Mutex
	routes  []application.RouteRequest
	replies []application.ChatReplyRequest
}

func (c *chatRT) Route(_ context.Context, in application.RouteRequest) (application.RouteResponse, error) {
	c.mu.Lock()
	c.routes = append(c.routes, in)
	c.mu.Unlock()
	return c.route(in)
}

func (c *chatRT) ChatReply(_ context.Context, in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
	c.mu.Lock()
	c.replies = append(c.replies, in)
	c.mu.Unlock()
	if in.Limit != "" {
		return application.ChatReplyResponse{Text: "LIMIT:" + in.Limit + ":" + in.Agent.ID}, nil
	}
	if c.reply != nil {
		return c.reply(in)
	}
	return application.ChatReplyResponse{Text: "dice " + in.Agent.ID, Usage: application.Usage{Model: "m", InputTokens: 300, OutputTokens: 40, CostUSD: 0.0004}}, nil
}

func (c *chatRT) speakers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.replies {
		if r.Limit == "" { // "I can't" lines are scripted and free: not a conversation turn
			out = append(out, r.Agent.ID)
		}
	}
	return out
}

func fixedRoute(intent, topic string, rs ...application.RouteResponder) func(application.RouteRequest) (application.RouteResponse, error) {
	return func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{Intent: intent, Topic: topic, Responders: rs, Source: "llm"}, nil
	}
}

func primary(id string) application.RouteResponder {
	return application.RouteResponder{AgentID: id, Role: "primary", Reason: "porque sí"}
}
func contributor(id string) application.RouteResponder {
	return application.RouteResponder{AgentID: id, Role: "contributor", Reason: "aporta"}
}

type guardFake struct {
	deny     map[string]string // agent id ("*" = everybody) -> code
	readOnly bool
}

func (g guardFake) Admit(_ context.Context, _, agentID string) application.GuardVerdict {
	if c, ok := g.deny["*"]; ok {
		return application.GuardVerdict{Code: c}
	}
	if c, ok := g.deny[agentID]; ok {
		return application.GuardVerdict{Code: c}
	}
	return application.GuardVerdict{Allowed: true}
}
func (g guardFake) SideEffectsBlocked(context.Context, string, string) bool { return g.readOnly }
func (guardFake) ToolBlocked(context.Context, string, string, string) bool  { return false }
func (guardFake) IsSideEffect(string, string) bool                          { return false }

type styleFake struct{ s application.RunStyle }

func (f styleFake) Style(context.Context) (application.RunStyle, error) { return f.s, nil }

// ---- helpers ----

func newChat(t *testing.T, rt application.Runtime, mutate func(*application.Config)) *harness {
	t.Helper()
	return newHarness(t, rt, func(c *application.Config) {
		c.ChatStagger = 0
		if mutate != nil {
			mutate(c)
		}
	})
}

func (h *harness) say(conv, text string) application.ChatTurn {
	h.t.Helper()
	turn, err := h.orch.PostChat(context.Background(), conv, text)
	if err != nil {
		h.t.Fatalf("PostChat: %v", err)
	}
	h.orch.Wait()
	return turn
}

func (h *harness) messages(conv string) []domain.Message {
	h.t.Helper()
	ms, err := h.store.ListMessages(context.Background(), domain.DemoOrgID, conv)
	if err != nil {
		h.t.Fatal(err)
	}
	return ms
}

// spoken returns the senders of the non-user messages of a conversation, in order.
func spoken(ms []domain.Message, kinds ...string) []string {
	var out []string
	for _, m := range ms {
		if m.From == "user" {
			continue
		}
		if len(kinds) > 0 && !contains(kinds, m.Kind) {
			continue
		}
		out = append(out, m.From)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (c *capture) of(typ string) []domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.Event
	for _, e := range c.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (h *harness) noWork() {
	h.t.Helper()
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	tasks, _ := h.store.ListTasks(context.Background(), domain.DemoOrgID, "", "")
	if len(reqs) != 0 || len(tasks) != 0 {
		h.t.Fatalf("a conversation must not create work: %d requests, %d tasks", len(reqs), len(tasks))
	}
	if n := h.pub.count(domain.EvRequestReceived) + h.pub.count(domain.EvTaskCreated) + h.pub.count(domain.EvPlanCreated); n != 0 {
		h.t.Fatalf("request/plan/task events emitted: %d", n)
	}
}

func newChatRuntime(route func(application.RouteRequest) (application.RouteResponse, error)) *chatRT {
	return &chatRT{fakeRuntime: &fakeRuntime{plan: proposalPlan()}, route: route}
}

// ---- smalltalk / question / 1:1 ----

func TestGreetingGetsSeveralGreetingsAndCreatesNoRequest(t *testing.T) {
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant"), contributor("legal"), contributor("sales")))
	h := newChat(t, rt, nil)
	turn := h.say("office", "hola")

	ms := h.messages("office")
	if got := spoken(ms); len(got) != 3 || got[0] != "assistant" {
		t.Fatalf("assistant first, then two colleagues: %v", got)
	}
	if ms[0].From != "user" || ms[0].ID != turn.MessageID || ms[0].TurnID != turn.TurnID {
		t.Fatalf("the user message is stored first: %+v", ms[0])
	}
	for _, m := range ms[1:] {
		if m.TurnID != turn.TurnID || m.ReplyTo == nil || *m.ReplyTo != turn.MessageID || m.Kind != "chat" {
			t.Fatalf("replies carry turn_id and reply_to: %+v", m)
		}
	}
	h.noWork()

	dec := h.pub.of(domain.EvRouteDecided)
	if len(dec) != 1 {
		t.Fatalf("route.decided events: %d", len(dec))
	}
	p := dec[0].Payload.(map[string]any)
	if p["turn_id"] != turn.TurnID || p["intent"] != "smalltalk" || len(p["responders"].([]application.RouteResponder)) != 3 {
		t.Fatalf("route.decided payload: %+v", p)
	}
	if n := h.pub.count(domain.EvChatMessage); n != 4 { // the user's message + 3 replies
		t.Fatalf("chat.message events: %d", n)
	}
	on, off := 0, 0
	for _, e := range h.pub.of(domain.EvChatTyping) {
		if e.Payload.(map[string]any)["on"] == true {
			on++
		} else {
			off++
		}
	}
	if on != 3 || off != 3 {
		t.Fatalf("typing on/off: %d/%d", on, off)
	}
	// small replies cost a fraction of a cent, are in the ledger and never in a request
	usage, _ := h.store.ListUsage(context.Background(), domain.DemoOrgID)
	if len(usage) != 3 || usage[0].Kind != domain.UsageChat || usage[0].RequestID != "" {
		t.Fatalf("usage ledger: %+v", usage)
	}
	if c, _ := h.store.OrgCost(context.Background(), domain.DemoOrgID); c != 0 {
		t.Fatalf("no request cost: %v", c)
	}
	// the agents are not left "talking"
	if a := h.agent("assistant"); a.State == domain.StateTalking {
		t.Fatalf("agent left talking")
	}
}

func TestFinanceQuestionOnlyAccountingAnswersAndLegalAddsOneLine(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "finance", primary("accounting"), contributor("legal")))
	h := newChat(t, rt, nil)
	h.say("office", "hablemos del balance")
	if got := spoken(h.messages("office")); len(got) != 2 || got[0] != "accounting" || got[1] != "legal" {
		t.Fatalf("accounting answers, legal adds one line after: %v", got)
	}
	rt.mu.Lock()
	second := rt.replies[1]
	rt.mu.Unlock()
	if second.ResponderRole != "contributor" || len(second.PriorReplies) != 1 || second.Slot != 1 {
		t.Fatalf("the contributor sees what was already said: %+v", second)
	}
	h.noWork()
}

func TestTheBackendCapsWhatTheRouterAsksFor(t *testing.T) {
	// A misbehaving router (or a prompt injection) cannot wake the whole office.
	rt := newChatRuntime(fixedRoute("question", "finance", primary("accounting"), contributor("legal"), contributor("hr"),
		contributor("sales"), contributor("ghost"), primary("accounting")))
	h := newChat(t, rt, nil)
	h.say("office", "balance")
	if got := spoken(h.messages("office")); len(got) != 2 {
		t.Fatalf("owner + at most one contributor: %v", got)
	}
	rt2 := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant"), contributor("hr"), contributor("sales"), contributor("legal"), contributor("analyst")))
	h2 := newChat(t, rt2, nil)
	h2.say("office", "hola")
	if got := spoken(h2.messages("office")); len(got) != 3 {
		t.Fatalf("greeting: assistant + two colleagues, not everybody: %v", got)
	}
}

func TestDirectChatOnlyThatAgentAnswers(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "finance", primary("sales"), contributor("hr"), contributor("accounting")))
	h := newChat(t, rt, nil)
	h.say("agent:legal", "hablemos del balance")
	if got := spoken(h.messages("agent:legal")); len(got) != 1 || got[0] != "legal" {
		t.Fatalf("only the addressed agent answers: %v", got)
	}
	if sp := rt.speakers(); len(sp) != 1 || sp[0] != "legal" {
		t.Fatalf("one runtime call: %v", sp)
	}
	c, err := h.store.GetConversation(context.Background(), domain.DemoOrgID, "agent:legal")
	if err != nil || len(c.Participants) != 2 {
		t.Fatalf("1:1 conversation: %+v %v", c, err)
	}
	if _, err := h.store.GetConversation(context.Background(), domain.DemoOrgID, "office"); err == nil {
		t.Fatalf("the office channel is not created by a 1:1 chat")
	}
	h.noWork()
}

func TestDirectChatOtherAreaBecomesAVisibleConsult(t *testing.T) {
	rt := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{Intent: "question", Topic: "finance", Responders: []application.RouteResponder{primary("legal")},
			Consult: &application.RouteConsult{AgentID: "accounting", Reason: "es de finanzas"}}, nil
	})
	rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		if in.Consult != nil {
			return application.ChatReplyResponse{Text: "El margen está bien.", Kind: "answer"}, nil
		}
		return application.ChatReplyResponse{Text: "Eso lo ve Tomás, se lo pregunto.",
			Consult: &application.ChatReplyConsult{ToAgentID: "accounting", Question: "¿Cómo está el margen?"}}, nil
	}
	h := newChat(t, rt, nil)
	h.say("agent:legal", "¿cómo está el margen?")
	ms := h.messages("agent:legal")
	var kinds []string
	for _, m := range ms {
		kinds = append(kinds, m.From+":"+m.Kind)
	}
	want := []string{"user:chat", "legal:chat", "legal:consult", "accounting:answer"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("consult flow: %v", kinds)
	}
	if ms[2].To != "accounting" || ms[3].To != "legal" {
		t.Fatalf("consult goes to the colleague, the answer comes back: %+v", ms[2:])
	}
	if got := rt.speakers(); len(got) != 2 {
		t.Fatalf("exactly one extra call for the colleague, nobody else: %v", got)
	}
	// the consult is an event of its own and shows in the activity feed
	if n := len(h.pub.of(domain.EvChatMessage)); n != 4 {
		t.Fatalf("chat.message events: %d", n)
	}
	h.noWork()
}

// ---- tasks ----

func TestTaskExplainsWhoAndWhyThenCreatesTheRequest(t *testing.T) {
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	rt.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Objectives: []string{"enviar propuesta"}, Tasks: []application.PlannedTask{
			{Key: "a", Title: "Preparar propuesta", AgentID: "sales", Reason: "conoce al cliente"},
			{Key: "b", Title: "Revisar margen", AgentID: "accounting", DependsOn: []string{"a"}}, // no reason: the backend explains by role
		}}, nil
	}
	h := newChat(t, rt, nil)
	turn := h.say("office", "Tengo un cliente nuevo por $50,000, prepara todo")

	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(reqs) != 1 || reqs[0].Status != domain.RequestDone {
		t.Fatalf("the normal flow ran: %+v", reqs)
	}
	tasks := h.tasks(reqs[0].ID)
	if tasks["Preparar propuesta"].AssignedReason != "conoce al cliente" {
		t.Fatalf("assigned_reason from the plan: %+v", tasks["Preparar propuesta"])
	}
	if r := tasks["Revisar margen"].AssignedReason; !strings.Contains(r, "márgenes") {
		t.Fatalf("default reason by role: %q", r)
	}
	created := h.pub.of(domain.EvTaskCreated)
	if len(created) != 2 || created[0].Payload.(map[string]any)["assigned_reason"] != "conoce al cliente" {
		t.Fatalf("task.created carries assigned_reason: %+v", created)
	}

	ms := h.messages("office")
	if got := spoken(ms); len(got) < 3 || got[0] != "assistant" || got[1] != "assistant" {
		t.Fatalf("ack, explanation, then the report notice, all from the assistant: %v", got)
	}
	explain := ms[2]
	for _, want := range []string{"Valeria Ríos", "Preparar propuesta", "conoce al cliente", "Tomás Vidal", "Revisar margen", "aprobación"} {
		if !strings.Contains(explain.Text, want) {
			t.Fatalf("the explanation must name who/what/why (%q missing):\n%s", want, explain.Text)
		}
	}
	if explain.RequestID == nil || *explain.RequestID != reqs[0].ID || explain.TurnID != turn.TurnID {
		t.Fatalf("explanation is linked to the request and the turn: %+v", explain)
	}
	last := ms[len(ms)-1]
	if last.RequestID == nil || !strings.Contains(last.Text, "informe") && !strings.Contains(last.Text, "Informe") {
		t.Fatalf("the chat hears when the report is ready: %+v", last)
	}
	// the human-in-the-loop flow is untouched: the plan went through the usual events
	if h.pub.count(domain.EvPlanCreated) != 1 || h.pub.count(domain.EvReportCreated) != 1 {
		t.Fatalf("plan/report events")
	}
}

func TestTaskRequestsStillGoThroughHumanApproval(t *testing.T) {
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	rt.fakeRuntime = sendProposalRuntime(proposalPlan())
	h := newChat(t, rt, nil)
	h.orch.PostChat(context.Background(), "agent:sales", "prepara la propuesta y envíala") //nolint:errcheck
	h.waitFor("approval", func() bool { _, ok := h.pendingApproval(); return ok })
	ap, _ := h.pendingApproval()
	if ap.Status != domain.ApprovalPending {
		t.Fatalf("approval pending: %+v", ap)
	}
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	// asked in a 1:1 chat: the addressed agent (not the assistant) confirms and explains
	got := spoken(h.messages("agent:sales"))
	if len(got) < 2 || got[0] != "sales" || got[1] != "sales" {
		t.Fatalf("the addressed agent confirms and explains: %v", got)
	}
}

// ---- degraded modes ----

func TestRuntimeWithoutChatSupportUsesLocalRulesAndCannedLines(t *testing.T) {
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h.say("office", "hola")
	got := spoken(h.messages("office"))
	if len(got) != 3 || got[0] != "assistant" {
		t.Fatalf("local rules still greet with the assistant and two colleagues: %v", got)
	}
	h.noWork()
	if h.pub.of(domain.EvRouteDecided)[0].Payload.(map[string]any)["source"] != "local" {
		t.Fatalf("route source should be local")
	}
	// the local router also separates topics and tasks
	h2 := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h2.say("office", "hablemos del balance")
	if got := spoken(h2.messages("office")); len(got) != 1 || got[0] != "accounting" {
		t.Fatalf("balance goes to accounting: %v", got)
	}
	h2.noWork()
}

func TestLocalRulesIntentTable(t *testing.T) {
	cases := []struct{ text, intent, first string }{
		{"hola", "smalltalk", "assistant"}, {"Buenos días equipo", "smalltalk", "assistant"}, {"gracias", "smalltalk", "assistant"},
		{"hablemos del balance", "question", "accounting"}, {"¿hay riesgo en el contrato?", "question", "legal"},
		{"¿cuándo podemos contratar a alguien?", "question", "hr"}, {"¿cómo van las ventas?", "question", "sales"},
		{"¿alcanza la capacidad?", "question", "operations"}, {"quiero ver las métricas", "question", "analyst"},
		{"cuéntame algo", "question", "assistant"},
		{"Tengo un cliente nuevo por $50,000, prepara todo", "task", "assistant"}, {"¿puedes revisar el contrato?", "task", "assistant"},
		{"calcula el margen de marzo", "task", "assistant"}, {"please prepare a proposal", "task", "assistant"},
	}
	for _, c := range cases {
		h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
		h.say("office", c.text)
		dec := h.pub.of(domain.EvRouteDecided)
		if len(dec) != 1 {
			t.Fatalf("%q: no route.decided", c.text)
		}
		p := dec[0].Payload.(map[string]any)
		rs := p["responders"].([]application.RouteResponder)
		if p["intent"] != c.intent || rs[0].AgentID != c.first || rs[0].Role != "primary" || rs[0].Reason == "" {
			t.Errorf("%q => %v %+v, want %s/%s", c.text, p["intent"], rs, c.intent, c.first)
		}
	}
}

func TestRuntimeDownMidTurnFallsBack(t *testing.T) {
	boom := errors.New("runtime down")
	rt := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{}, boom
	})
	rt.reply = func(application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		return application.ChatReplyResponse{}, boom
	}
	h := newChat(t, rt, nil)
	h.say("office", "hola")
	ms := h.messages("office")
	if got := spoken(ms); len(got) == 0 || got[0] != "assistant" {
		t.Fatalf("the assistant still answers something: %v", got)
	}
	h.noWork()
	if !h.hasAudit("chat.route_fallback") || !h.hasAudit("chat.reply_fallback") {
		t.Fatalf("fallbacks are audited")
	}
	// nothing was billed for failed calls
	if u, _ := h.store.ListUsage(context.Background(), domain.DemoOrgID); len(u) != 0 {
		t.Fatalf("no usage: %+v", u)
	}

	// a question while the runtime is down: honest line, no invention
	h2 := newChat(t, rt, nil)
	h2.say("office", "hablemos del balance")
	ms2 := h2.messages("office")
	if len(ms2) != 2 || ms2[1].From != "accounting" || !strings.Contains(ms2[1].Text, "no puedo") {
		t.Fatalf("honest fallback line: %+v", ms2)
	}
	// and a task is still turned into a request, whose planning fails visibly like before
	rt3 := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{}, boom
	})
	rt3.reply = rt.reply
	rt3.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{}, boom
	}
	h3 := newChat(t, rt3, func(c *application.Config) { c.MaxRetries = 1 })
	h3.say("office", "prepara una propuesta para Acme")
	reqs, _ := h3.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(reqs) != 1 || reqs[0].Status != domain.RequestFailed {
		t.Fatalf("request failed visibly: %+v", reqs)
	}
	last := h3.messages("office")
	if m := last[len(last)-1]; m.From != "system" || m.RequestID == nil {
		t.Fatalf("the failure is echoed in the chat: %+v", m)
	}
}

func TestInvalidRoutingFallsBackToRules(t *testing.T) {
	for name, resp := range map[string]application.RouteResponse{
		"unknown intent": {Intent: "chitchat", Responders: []application.RouteResponder{primary("assistant")}},
		"no responders":  {Intent: "question"},
		"unknown agents": {Intent: "question", Responders: []application.RouteResponder{primary("ghost")}},
	} {
		resp := resp
		rt := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) { return resp, nil })
		h := newChat(t, rt, nil)
		h.say("office", "hablemos del balance")
		if got := spoken(h.messages("office")); len(got) != 1 || got[0] != "accounting" {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// ---- controls and budget ----

func TestKillSwitchSilencesTheChatAndCreatesNoWork(t *testing.T) {
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	h := newChat(t, rt, nil)
	h.orch.SetConnections(guardFake{deny: map[string]string{"*": "kill_switch_active"}}, nil, nil)
	h.say("office", "prepara una propuesta")
	ms := h.messages("office")
	if got := spoken(ms); len(got) != 1 || got[0] != "assistant" || ms[1].Text != "LIMIT:kill_switch:assistant" {
		t.Fatalf("one limit line in the assistant's own voice, nobody else speaks: %v %+v", got, ms)
	}
	h.noWork()
	if len(rt.speakers()) != 0 {
		t.Fatalf("the runtime must not be called under the kill switch")
	}
	if !h.hasAudit("chat.blocked") {
		t.Fatalf("blocked chat is audited")
	}
}

func TestPausedAgentDoesNotAnswerButOthersStillDo(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "finance", primary("accounting"), contributor("legal")))
	h := newChat(t, rt, nil)
	h.orch.SetConnections(guardFake{deny: map[string]string{"legal": "agent_paused"}}, nil, nil)
	h.say("office", "hablemos del balance")
	if got := spoken(h.messages("office")); len(got) != 1 || got[0] != "accounting" {
		t.Fatalf("a paused side voice is skipped silently: %v", got)
	}
	h2 := newChat(t, rt, nil)
	h2.orch.SetConnections(guardFake{deny: map[string]string{"legal": "agent_paused"}}, nil, nil)
	h2.say("agent:legal", "hola")
	ms := h2.messages("agent:legal")
	if len(ms) != 2 || ms[1].From != "legal" || ms[1].Text != "LIMIT:paused:legal" {
		t.Fatalf("the agent they asked says, in its own voice, that it is paused: %+v", ms)
	}
	if a := h.agent("legal"); a.State == domain.StateTalking {
		t.Fatalf("paused agent must not animate as talking")
	}
}

func TestAgentBudgetCapStopsTheChat(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "finance", primary("accounting"), contributor("legal")))
	h := newChat(t, rt, nil)
	if err := h.store.SetBudgetCap(context.Background(), domain.DemoOrgID, domain.ScopeAgent, "accounting", 0.000001); err != nil {
		t.Fatal(err)
	}
	h.say("office", "hablemos del balance")
	ms := h.messages("office")
	if got := spoken(ms); len(got) != 1 || got[0] != "accounting" || ms[1].Text != "LIMIT:budget:accounting" {
		t.Fatalf("budget limit line instead of an answer, and the turn ends: %v", got)
	}
	if len(rt.speakers()) != 0 {
		t.Fatalf("no runtime call once the cap is hit")
	}
	if !h.hasAudit("chat.budget_exceeded") {
		t.Fatalf("budget stop is audited")
	}
}

func TestOrganizationBudgetCountsChatSpend(t *testing.T) {
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant")))
	rt.reply = func(application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		return application.ChatReplyResponse{Text: "hola", Usage: application.Usage{Model: "m", InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01}}, nil
	}
	h := newChat(t, rt, func(c *application.Config) { c.BudgetUSD = 0.02 })
	h.say("office", "hola") // spends ~$0.01 of a $0.02 organization budget
	h.say("office", "hola de nuevo")
	h.say("office", "hola otra vez")
	var notices int
	for _, m := range h.messages("office") {
		if strings.HasPrefix(m.Text, "LIMIT:budget") {
			notices++
		}
	}
	if notices == 0 || len(rt.speakers()) >= 3 {
		t.Fatalf("chat spend must eat the organization budget (notices=%d calls=%d)", notices, len(rt.speakers()))
	}
}

// ---- i18n ----

func TestEnglishOrganizationGetsEnglishTexts(t *testing.T) {
	h := newChat(t, &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Prepare proposal", AgentID: "sales"}}}, nil
	}}, nil)
	h.orch.SetStyle(styleFake{application.RunStyle{Locale: "en"}})
	h.say("office", "please prepare a proposal for Acme")
	var all []string
	for _, m := range h.messages("office") {
		all = append(all, m.Text)
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"I'll coordinate it with the team", "Here is what each person will do", "Why: owns the client relationship"} {
		if !strings.Contains(text, want) {
			t.Fatalf("english text %q missing:\n%s", want, text)
		}
	}
	c, _ := h.store.GetConversation(context.Background(), domain.DemoOrgID, "office")
	if c.Title != "Office" {
		t.Fatalf("title: %q", c.Title)
	}
}

func TestLocaleAndToneReachTheRuntime(t *testing.T) {
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant")))
	h := newChat(t, rt, nil)
	h.orch.SetStyle(styleFake{application.RunStyle{Locale: "en", Tone: "mx", AgentTones: map[string]string{"assistant": "ar"}}})
	h.say("office", "hi")
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.routes[0].Locale != "en" || rt.routes[0].Tone != "mx" || rt.replies[0].Locale != "en" || rt.replies[0].Tone != "ar" {
		t.Fatalf("locale/tone: %+v %+v", rt.routes[0], rt.replies[0])
	}
	if len(rt.routes[0].Agents) != 7 || rt.routes[0].Conversation != "office" {
		t.Fatalf("route request: %+v", rt.routes[0])
	}
}

func TestHistoryIsSentToTheRuntime(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "general", primary("assistant")))
	h := newChat(t, rt, nil)
	h.say("office", "primera")
	h.say("office", "segunda")
	rt.mu.Lock()
	defer rt.mu.Unlock()
	hist := rt.routes[1].History
	if len(hist) != 2 || hist[0].From != "user" || hist[0].Text != "primera" || hist[1].From != "assistant" {
		t.Fatalf("history of the second turn: %+v", hist)
	}
}

// ---- validation and paging ----

func TestPostChatValidation(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant"))), nil)
	ctx := context.Background()
	for name, c := range map[string]struct {
		conv, text string
		want       error
	}{
		"empty text":        {"office", "   ", domain.ErrInvalid},
		"bad conversation":  {"random", "hola", domain.ErrInvalid},
		"bare agent prefix": {"agent:", "hola", domain.ErrInvalid},
		"unknown agent":     {"agent:ghost", "hola", domain.ErrNotFound},
		"too long":          {"office", strings.Repeat("x", 4001), domain.ErrInvalid},
	} {
		if _, err := h.orch.PostChat(ctx, c.conv, c.text); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	h.orch.Wait()
	if h.pub.count(domain.EvChatMessage) != 0 || h.pub.count(domain.EvRouteDecided) != 0 {
		t.Fatalf("rejected input emits nothing")
	}
}

func TestChatMessagesPaging(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("question", "general", primary("assistant"))), nil)
	for _, s := range []string{"uno", "dos", "tres"} {
		h.say("office", s)
	} // 6 messages: user/assistant x3
	ctx := context.Background()
	page, more, err := h.q.ChatMessages(ctx, "office", "", 4)
	if err != nil || len(page) != 4 || !more || page[3].From != "assistant" || page[0].Text != "dos" {
		t.Fatalf("newest page: %v %v %+v", err, more, page)
	}
	older, more, err := h.q.ChatMessages(ctx, "office", page[0].ID, 4)
	if err != nil || len(older) != 2 || more || older[0].Text != "uno" {
		t.Fatalf("older page: %v %v %+v", err, more, older)
	}
	all, more, err := h.q.ChatMessages(ctx, "office", "", 0)
	if err != nil || len(all) != 6 || more {
		t.Fatalf("legacy: all messages: %v %v %d", err, more, len(all))
	}
	if _, _, err := h.q.ChatMessages(ctx, "office", "nope", 4); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown cursor: %v", err)
	}
	empty, _, err := h.q.ChatMessages(ctx, "agent:hr", "", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("a chat that never started is empty, not 404: %v %v", err, empty)
	}
	if _, _, err := h.q.ChatMessages(ctx, "agent:ghost", "", 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	if _, _, err := h.q.ChatMessages(ctx, "no-such-conversation", "", 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown conversation: %v", err)
	}
}

func TestChatDoesNotBreakTheClassicRequestFlow(t *testing.T) {
	// POST /requests keeps working exactly as before (the $50,000 smoke scenario).
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	id, err := h.orch.Submit(context.Background(), "Tengo un cliente nuevo por $50,000, prepara todo")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if h.request(id).Status != domain.RequestDone {
		t.Fatalf("request: %+v", h.request(id))
	}
	if h.pub.count(domain.EvChatMessage) != 0 {
		t.Fatalf("a request submitted directly does not talk in the chat")
	}
	for _, task := range h.tasks(id) {
		if task.AssignedReason == "" {
			t.Fatalf("every task explains its assignment, also without chat: %+v", task)
		}
	}
}

// ---- out of competence, handoffs and limits ----

func TestPlainSystemNoticesWhenTheRuntimeCannotVoiceALimit(t *testing.T) {
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h.orch.SetConnections(guardFake{deny: map[string]string{"*": "kill_switch_active"}}, nil, nil)
	h.say("office", "prepara una propuesta")
	ms := h.messages("office")
	if len(ms) != 2 || ms[1].From != "system" || !strings.Contains(ms[1].Text, "pausa") {
		t.Fatalf("fallback: one system notice: %+v", ms)
	}
	h.noWork()
}

func foreignRoute(owner string) func(application.RouteRequest) (application.RouteResponse, error) {
	return func(in application.RouteRequest) (application.RouteResponse, error) {
		intent := "question"
		if strings.Contains(in.Text, "propuesta") {
			intent = "task"
		}
		resp := application.RouteResponse{Intent: intent, Topic: "sales", Responders: []application.RouteResponder{primary("accounting")}}
		if strings.Contains(in.Text, "ventas") || intent == "task" { // only foreign topics are redirected
			resp.Consult = &application.RouteConsult{AgentID: owner, Reason: "es de ventas"}
		}
		return resp, nil
	}
}

func TestForeignTaskIsDeclinedByNameAndReassignedWithAReason(t *testing.T) {
	rt := newChatRuntime(foreignRoute("sales"))
	rt.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{
			{Key: "a", Title: "Armar la propuesta comercial", AgentID: "accounting", Reason: "me la pidieron a mí"},
			{Key: "b", Title: "Calcular el margen", Description: "costos y margen", AgentID: "accounting", DependsOn: []string{"a"}},
		}}, nil
	}
	h := newChat(t, rt, nil)
	h.say("agent:accounting", "véndeme una propuesta para Acme")

	rt.mu.Lock()
	first := rt.replies[0]
	rt.mu.Unlock()
	if first.Intent != "task" || first.ConsultTo != "sales" || first.Agent.ID != "accounting" {
		t.Fatalf("the accountant is told who owns the work: %+v", first)
	}
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(reqs) != 1 {
		t.Fatalf("the request is still created (human approval flow intact): %+v", reqs)
	}
	tasks := h.tasks(reqs[0].ID)
	moved := tasks["Armar la propuesta comercial"]
	if moved.AgentID != "sales" || !strings.Contains(moved.AssignedReason, "Reasignada") ||
		!strings.Contains(moved.AssignedReason, "Tomás Vidal") || !strings.Contains(moved.AssignedReason, "Valeria Ríos") {
		t.Fatalf("reassigned to sales with the reason: %+v", moved)
	}
	if kept := tasks["Calcular el margen"]; kept.AgentID != "accounting" {
		t.Fatalf("the part that really is theirs stays with them: %+v", kept)
	}
	if !h.hasAudit("chat.task_reassigned") {
		t.Fatalf("reassignment is audited")
	}
	var explain domain.Message
	for _, m := range h.messages("agent:accounting") {
		if m.RequestID != nil && strings.Contains(m.Text, "Reasignada") {
			explain = m
		}
	}
	if explain.From != "accounting" || !strings.Contains(explain.Text, "Valeria Ríos (Gerente de Ventas)") {
		t.Fatalf("the explanation shows the change: %+v", explain)
	}
}

func TestAssistantNeverDeclinesATask(t *testing.T) {
	rt := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
		return application.RouteResponse{Intent: "task", Responders: []application.RouteResponder{primary("assistant")},
			Consult: &application.RouteConsult{AgentID: "sales"}}, nil
	})
	rt.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Organizar", AgentID: "assistant"}}}, nil
	}
	h := newChat(t, rt, nil)
	h.say("agent:assistant", "prepara una propuesta de ventas")
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	for _, task := range h.tasks(reqs[0].ID) {
		if task.AgentID != "assistant" {
			t.Fatalf("the assistant coordinates, nothing is reassigned: %+v", task)
		}
	}
}

func TestRedirectOfferThenAcceptanceLetsTheColleagueTakeTheTurn(t *testing.T) {
	rt := newChatRuntime(foreignRoute("sales"))
	rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		switch {
		case in.Handoff:
			return application.ChatReplyResponse{Text: "te paso con " + in.ConsultTo}, nil
		case in.Consult != nil:
			return application.ChatReplyResponse{Text: "respuesta a la consulta"}, nil
		case in.Agent.ID == "accounting":
			return application.ChatReplyResponse{Text: "eso es de Valeria", Consult: &application.ChatReplyConsult{ToAgentID: "sales", Question: "¿cómo van las ventas?"}}, nil
		}
		return application.ChatReplyResponse{Text: "así van las ventas: " + in.Text}, nil
	}
	h := newChat(t, rt, nil)
	h.say("agent:accounting", "¿cómo van las ventas?")
	if got := spoken(h.messages("agent:accounting")); len(got) != 3 { // redirect, consult, answer
		t.Fatalf("redirect + visible consult + answer: %v", got)
	}
	h.say("agent:accounting", "sí, pásaselo")
	ms := h.messages("agent:accounting")
	tail := ms[len(ms)-2:]
	if tail[0].From != "accounting" || tail[0].Text != "te paso con sales" ||
		tail[1].From != "sales" || tail[1].Text != "así van las ventas: ¿cómo van las ventas?" {
		t.Fatalf("handoff line, then the colleague answers the ORIGINAL question: %+v", tail)
	}
	dec := h.pub.of(domain.EvRouteDecided)
	if p := dec[len(dec)-1].Payload.(map[string]any); p["source"] != "handoff" {
		t.Fatalf("route.decided for the handoff: %+v", p)
	}
	// the offer is consumed: a plain "sí" later does nothing special
	h.say("agent:accounting", "sí")
	dec = h.pub.of(domain.EvRouteDecided)
	if p := dec[len(dec)-1].Payload.(map[string]any); p["source"] == "handoff" {
		t.Fatalf("a handoff is single use")
	}
	h.noWork()
}

func TestRedirectOfferExpiresWhenTheUserChangesTopic(t *testing.T) {
	rt := newChatRuntime(foreignRoute("sales"))
	rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		if in.Agent.ID == "accounting" && in.Consult == nil && in.ConsultTo != "" {
			return application.ChatReplyResponse{Text: "eso es de Valeria", Consult: &application.ChatReplyConsult{ToAgentID: "sales", Question: "q"}}, nil
		}
		return application.ChatReplyResponse{Text: "ok"}, nil
	}
	h := newChat(t, rt, nil)
	h.say("agent:accounting", "¿cómo van las ventas?")
	h.say("agent:accounting", "mejor háblame del balance")
	h.say("agent:accounting", "sí")
	for _, e := range h.pub.of(domain.EvRouteDecided) {
		if e.Payload.(map[string]any)["source"] == "handoff" {
			t.Fatalf("an expired offer must not be accepted")
		}
	}
}

func TestOfficeMessageToOneColleagueByNameIsAnsweredByThemAndForeignTasksAreReassigned(t *testing.T) {
	// no chat support in the runtime: the local rules do it all
	h := newChat(t, &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Preparar la propuesta", AgentID: "accounting"}}}, nil
	}}, nil)
	h.say("office", "Tomás, véndeme una propuesta para Acme")
	dec := h.pub.of(domain.EvRouteDecided)[0].Payload.(map[string]any)
	rs := dec["responders"].([]application.RouteResponder)
	if dec["intent"] != "task" || len(rs) != 1 || rs[0].AgentID != "accounting" {
		t.Fatalf("only the addressed colleague answers: %+v", dec)
	}
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(reqs) != 1 || h.tasks(reqs[0].ID)["Preparar la propuesta"].AgentID != "sales" {
		t.Fatalf("reassigned to sales: %+v", h.tasks(reqs[0].ID))
	}
	h2 := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h2.say("office", "Tomás, ¿cómo están las ventas?")
	if got := spoken(h2.messages("office")); len(got) != 1 || got[0] != "accounting" {
		t.Fatalf("addressed colleague only: %v", got)
	}
	h2.noWork()
}

func TestReadOnlyModeIsSaidByTheAgentButTheWorkContinues(t *testing.T) {
	rt := newChatRuntime(fixedRoute("task", "sales", primary("assistant")))
	h := newChat(t, rt, nil)
	h.orch.SetConnections(guardFake{readOnly: true}, nil, nil)
	h.say("office", "prepara una propuesta")
	var texts []string
	for _, m := range h.messages("office") {
		texts = append(texts, m.Text)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "LIMIT:read_only:assistant") {
		t.Fatalf("read-only line missing: %v", texts)
	}
	if reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID); len(reqs) != 1 {
		t.Fatalf("read-only does not block planning")
	}
}

// ---- behaviour sweep regressions (docs/architecture/chat-routing.md, section 13) ----

func TestSanitizeChatText(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"plain":           {"  hola  ", "hola"},
		"nul and escape":  {"a\x00b\x1b[31m hola", "ab[31m hola"},
		"bidi override":   {"\u202ehola\u202c", "hola"},
		"zero width":      {"ho\u200bla\ufeff", "hola"},
		"emoji joiner":    {"👩\u200d💻", "👩\u200d💻"},
		"keeps newlines":  {"a\n\tb", "a\n\tb"},
		"carriage return": {"a\r\nb", "a\nb"},
		"invalid utf8":    {"ho\xffla", "hola"},
		"only control":    {"\x00\x01\u202e", ""},
	} {
		if got := application.SanitizeChatText(c.in); got != c.want {
			t.Errorf("%s: %q => %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestPostChatStoresSanitizedTextAndRejectsControlOnlyText(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant"))), nil)
	h.say("office", "ho\x00la\u202e")
	if ms := h.messages("office"); ms[0].Text != "hola" {
		t.Fatalf("the stored message must be clean: %q", ms[0].Text)
	}
	if _, err := h.orch.PostChat(context.Background(), "office", "\x00\u200b\u202e"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("text with nothing visible is invalid: %v", err)
	}
}

func TestRuntimeRequestsNeverCarryNullLists(t *testing.T) {
	// Go marshals a nil slice as null; the runtime validates lists strictly (this broke every "I can't" line once).
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant")))
	h := newChat(t, rt, nil)
	h.orch.SetConnections(guardFake{deny: map[string]string{"*": "kill_switch_active"}}, nil, nil)
	h.say("office", "hola")
	h2 := newChat(t, rt, nil)
	h2.say("agent:accounting", "hola")
	if len(rt.replies) == 0 || len(rt.routes) == 0 {
		t.Fatalf("expected runtime calls: %d replies, %d routes", len(rt.replies), len(rt.routes))
	}
	for _, r := range rt.replies {
		raw, _ := json.Marshal(r)
		for _, f := range []string{`"history":null`, `"prior_replies":null`, `"agents":null`} {
			if strings.Contains(string(raw), f) {
				t.Errorf("chat-reply carries %s: %s", f, raw)
			}
		}
	}
	for _, r := range rt.routes {
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), `"history":null`) || strings.Contains(string(raw), `"agents":null`) {
			t.Errorf("route carries a null list: %s", raw)
		}
	}
}

func TestLocalRulesSweepRegressions(t *testing.T) {
	cases := []struct{ text, intent, first string }{
		{"buen día", "smalltalk", "assistant"}, {"buen dia a todos", "smalltalk", "assistant"}, {"hola a todo el equipo", "smalltalk", "assistant"},
		{"👍", "smalltalk", "assistant"}, {"👋", "smalltalk", "assistant"},
		{"¿necesitamos un NDA?", "question", "legal"}, {"necesitamos un NDA para el cliente", "task", "assistant"},
		{"ayúdame con el contrato", "task", "assistant"}, {"¿cuánto nos cuesta el servicio?", "question", "accounting"},
		{"cuánta lana tenemos en caja", "question", "accounting"}, {"¿quién es el contador?", "question", "accounting"},
		{"how are the sales going", "question", "sales"}, {"contrata a un desarrollador", "task", "assistant"},
	}
	for _, c := range cases {
		h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
		h.say("office", c.text)
		p := h.pub.of(domain.EvRouteDecided)[0].Payload.(map[string]any)
		rs := p["responders"].([]application.RouteResponder)
		if p["intent"] != c.intent || rs[0].AgentID != c.first {
			t.Errorf("%q => %v %+v, want %s/%s", c.text, p["intent"], rs, c.intent, c.first)
		}
		if c.intent == "smalltalk" {
			h.noWork()
		}
	}
}

func TestLocalRulesFollowUpKeepsTheLastAnswerer(t *testing.T) {
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h.say("office", "¿cómo van las ventas?")
	h.say("office", "¿y por qué?")
	dec := h.pub.of(domain.EvRouteDecided)
	last := dec[len(dec)-1].Payload.(map[string]any)["responders"].([]application.RouteResponder)
	if last[0].AgentID != "sales" {
		t.Fatalf("a follow-up without a topic stays with sales: %+v", last)
	}
}

func TestCannedRedirectNamesTheColleagueWhenTheRuntimeIsDown(t *testing.T) {
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h.say("agent:accounting", "¿cómo van las ventas?")
	ms := h.messages("agent:accounting")
	if len(ms) < 2 || !strings.Contains(ms[1].Text, h.agent("sales").Name) {
		t.Fatalf("the canned line must point to the colleague by name: %+v", ms)
	}
}

func TestPostChatIsIdempotentWithAKey(t *testing.T) {
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant")))
	h := newChat(t, rt, nil)
	ctx := context.Background()
	t1, replayed, err := h.orch.PostChatKeyed(ctx, "office", "hola", "k1")
	if err != nil || replayed {
		t.Fatalf("first post: %v replayed=%v", err, replayed)
	}
	t2, replayed, err := h.orch.PostChatKeyed(ctx, "office", "hola", "k1")
	if err != nil || !replayed || t2 != t1 {
		t.Fatalf("a retry with the same key returns the first turn: %+v vs %+v replayed=%v err=%v", t2, t1, replayed, err)
	}
	t3, replayed, _ := h.orch.PostChatKeyed(ctx, "office", "hola", "k2")
	if replayed || t3.TurnID == t1.TurnID {
		t.Fatalf("a different key is a new message")
	}
	if _, replayed, _ = h.orch.PostChatKeyed(ctx, "agent:sales", "hola", "k1"); replayed {
		t.Fatalf("the key is scoped to the conversation")
	}
	if _, _, err := h.orch.PostChatKeyed(ctx, "office", "hola", strings.Repeat("k", 129)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an oversized key is a 400: %v", err)
	}
	h.orch.Wait()
	users := 0
	for _, m := range h.messages("office") {
		if m.From == "user" {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("the duplicate must not be stored twice: %d user messages", users)
	}
}

func TestLegacyMessageEndpointAppliesTheSameLimits(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant"))), nil)
	if _, err := h.orch.PostUserMessage(context.Background(), "office", strings.Repeat("x", 4001)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("too long: %v", err)
	}
	if _, err := h.orch.PostUserMessage(context.Background(), "office", "\x00\u200b"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invisible text: %v", err)
	}
}

func TestTurnsOfOneConversationRunInOrderAndSeeTheirPast(t *testing.T) {
	release := make(chan struct{})
	first := make(chan struct{}, 1)
	rt := newChatRuntime(fixedRoute("smalltalk", "greeting", primary("assistant")))
	rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
		select {
		case first <- struct{}{}: // only the very first reply waits
			<-release
		default:
		}
		return application.ChatReplyResponse{Text: "re: " + in.Text}, nil
	}
	h := newChat(t, rt, nil)
	ctx := context.Background()
	if _, err := h.orch.PostChat(ctx, "office", "uno"); err != nil {
		t.Fatal(err)
	}
	<-first // the first turn is now speaking
	if _, err := h.orch.PostChat(ctx, "office", "dos"); err != nil {
		t.Fatal(err)
	}
	if got := len(rt.speakers()); got != 1 {
		t.Fatalf("the second turn must wait for the first: %d replies so far", got)
	}
	close(release)
	h.orch.Wait()
	var seq []string
	for _, m := range h.messages("office") {
		seq = append(seq, m.Text)
	}
	if strings.Join(seq, "|") != "uno|dos|re: uno|re: dos" && strings.Join(seq, "|") != "uno|re: uno|dos|re: dos" {
		t.Fatalf("answers must not interleave between turns: %v", seq)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.routes) != 2 {
		t.Fatalf("routes: %d", len(rt.routes))
	}
	hist := rt.routes[1].History
	if len(hist) != 2 || hist[0].Text != "uno" || hist[1].Text != "re: uno" {
		t.Fatalf("the second turn sees the first one (and not its own message): %+v", hist)
	}
}
