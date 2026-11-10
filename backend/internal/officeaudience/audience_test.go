package officeaudience_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/officeaudience"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/roles"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// officesFix: Privada (private office, member u-pri, agent legal), Abierta (open, member u-ana, agent sales), the
// other agents in General; u-boss is an admin in no office and u-eve a plain person (who may use legal from the
// directory, say).
func officesFix(t *testing.T, ctx context.Context, org string, st application.Store) *offices.Service {
	t.Helper()
	osvc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-open", Name: "Abierta"}, {ID: "d-priv", Name: "Privada"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-ana": "d-open", "u-pri": "d-priv"}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"sales": "d-open", "legal": "d-priv"}, nil
		},
		Agents: func(c context.Context, o string) ([]offices.AgentRef, error) {
			as, err := st.ListAgents(c, o)
			out := []offices.AgentRef{}
			for _, a := range as {
				out = append(out, offices.AgentRef{ID: a.ID})
			}
			return out, err
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-ana", Name: "Ana"},
				{UserID: "u-pri", Name: "Pri"}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	if _, err := osvc.Enable(ctx, org, boss, "d-open", offices.Open); err != nil {
		t.Fatal(err)
	}
	if _, err := osvc.Enable(ctx, org, boss, "d-priv", offices.Private); err != nil {
		t.Fatal(err)
	}
	return osvc
}

// fix: a request r-priv with a task of legal, asked for by u-eve; r-gen with a task of operations (General).
type fix struct {
	ctx     context.Context
	org     string
	rec     *application.Recorder
	store   *memory.Store
	osvc    *offices.Service
	hub     *events.Hub
	sockets map[string]*events.Client
}

func newFix(t *testing.T) *fix {
	t.Helper()
	cfg := application.DefaultConfig()
	org := cfg.OrgID
	ctx := application.WithOrg(context.Background(), org)
	st := memory.New()
	if err := st.Seed(ctx, domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, r := range []string{"r-priv", "r-gen", "r-new"} {
		if err := st.CreateRequest(ctx, org, domain.Request{ID: r, Text: "pedido " + r, Status: domain.RequestRunning, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tk := range []domain.Task{{ID: "t-priv", RequestID: "r-priv", AgentID: "legal", Title: "cuentas"},
		{ID: "t-gen", RequestID: "r-gen", AgentID: "operations", Title: "agenda"}} {
		if err := st.CreateTask(ctx, org, tk); err != nil {
			t.Fatal(err)
		}
	}
	rp := "r-priv"
	if err := st.CreateConversation(ctx, org, domain.Conversation{ID: "c-priv", Participants: []string{"user", "assistant"}, RequestID: &rp}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRunMeta(ctx, org, application.RunMeta{RequestID: "r-priv", RequestedBy: "u-eve"}); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(quiet)
	f := &fix{ctx: ctx, org: org, store: st, hub: hub, sockets: map[string]*events.Client{}}
	for _, u := range []string{"u-boss", "u-ana", "u-pri", "u-eve"} {
		f.sockets[u] = hub.RegisterUser(org, u)
	}
	f.osvc = officesFix(t, ctx, org, st)
	f.rec = &application.Recorder{OrgID: org, Store: st, Pub: events.LocalBus{Hub: hub}, Log: quiet}
	return f
}

// install wires the rule with a real orchestrator over the store (its RequestedBy reads the run meta).
func (f *fix) install(rt application.Runtime) *application.Orchestrator {
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	q := &application.Queries{Store: f.store, Cfg: cfg}
	orch := application.NewOrchestrator(context.Background(), cfg, f.store, rt, memory.NewLocker(), f.rec,
		application.NewApprovals(cfg, f.store, f.rec), q, quiet)
	f.rec.SetAudienceFor(officeaudience.New(f.osvc, f.store, func(c context.Context, org, id string) string {
		return orch.RequestedBy(application.WithOrg(c, org), id)
	}, quiet))
	return orch
}

// got drains every socket and returns, per user, the event types they received.
func (f *fix) got() map[string][]string {
	out := map[string][]string{}
	for u, c := range f.sockets {
		for {
			select {
			case raw := <-c.Send:
				var ev domain.Event
				_ = json.Unmarshal(raw, &ev)
				out[u] = append(out[u], ev.Type)
				continue
			default:
			}
			break
		}
	}
	return out
}

func (f *fix) saved() []domain.Event {
	evs, _ := f.store.ListEvents(f.ctx, f.org, domain.EventQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})
	return evs
}

func TestPrivateAgentEventsReachOnlyMembers(t *testing.T) {
	task := domain.Task{ID: "t-priv", RequestID: "r-priv", AgentID: "legal", Title: "cuentas"}
	cases := []struct {
		name string
		ctx  func(context.Context) context.Context
		act  application.Action
	}{
		{"message.sent of the agent", nil, application.Action{Type: domain.EvMessageSent, AgentID: "legal", Text: "legal → assistant: listo",
			Payload: map[string]any{"message": domain.Message{ID: "m1", ConversationID: "c-priv", From: "legal", To: "assistant", Kind: "delegation", Text: "listo"}}}},
		{"message.sent of the user in the request's conversation", nil, application.Action{Type: domain.EvMessageSent,
			Payload: map[string]any{"message": domain.Message{ID: "m2", ConversationID: "c-priv", From: "user", To: "assistant", Kind: "chat", Text: "secreto"}}}},
		{"task.completed", nil, application.Action{Type: domain.EvTaskCompleted, AgentID: "legal", Text: "Tarea completada",
			Payload: map[string]any{"task": task}}},
		{"report.created (assistant, private request)", nil, application.Action{Type: domain.EvReportCreated, AgentID: "assistant", Text: "Informe",
			Payload: map[string]any{"report": domain.Report{ID: "rep1", RequestID: "r-priv", Title: "Cierre"}}}},
		{"approval.requested", nil, application.Action{Type: domain.EvApprovalRequest, AgentID: "legal", Text: "Aprobación requerida",
			Payload: map[string]any{"approval": domain.Approval{ID: "ap1", TaskID: "t-priv", AgentID: "legal", Title: "pagar"}}}},
		{"request.received addressed to the agent", func(c context.Context) context.Context {
			return application.WithWorkPeople(application.WithWorkAgents(c, "legal"), "u-eve")
		}, application.Action{Type: domain.EvRequestReceived, Text: "Nuevo pedido", Payload: map[string]any{"request_id": "r-new", "text": "secreto"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFix(t)
			f.install(nil)
			ctx := f.ctx
			if c.ctx != nil {
				ctx = c.ctx(ctx)
			}
			f.rec.Emit(ctx, c.act)
			got := f.got()
			if !slices.Contains(got["u-pri"], c.act.Type) {
				t.Errorf("the member must receive it: %v", got)
			}
			if !slices.Contains(got["u-eve"], c.act.Type) {
				t.Errorf("whoever asked for the request must receive it: %v", got)
			}
			for _, u := range []string{"u-boss", "u-ana"} {
				if len(got[u]) != 0 {
					t.Errorf("%s (not a member) received %v", u, got[u])
				}
			}
			for _, ev := range f.saved() {
				if ev.Type == c.act.Type {
					t.Errorf("a private office event must stay out of the organization's event log: %+v", ev)
				}
			}
			if c.act.Text != "" {
				if !slices.Contains(got["u-pri"], domain.EvActivityLogged) {
					t.Errorf("the member must receive the activity line live: %v", got["u-pri"])
				}
				items, _ := f.store.ListActivity(f.ctx, f.org, "", 10)
				if len(items) != 1 || items[0].AgentID == nil || *items[0].AgentID != "legal" {
					t.Errorf("the activity line must be kept under the private agent that sealed it: %+v", items)
				}
			}
		})
	}
}

func TestGeneralAgentEventsReachEveryone(t *testing.T) {
	f := newFix(t)
	f.install(nil)
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskCompleted, AgentID: "operations", Text: "Tarea completada",
		Payload: map[string]any{"task": domain.Task{ID: "t-gen", RequestID: "r-gen", AgentID: "operations"}}})
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskCompleted, AgentID: "sales",
		Payload: map[string]any{"task": domain.Task{ID: "t-open", AgentID: "sales"}}})
	got := f.got()
	for _, u := range []string{"u-boss", "u-ana", "u-pri", "u-eve"} {
		if n := len(got[u]); n != 3 { // two task.completed + one activity.logged
			t.Errorf("%s received %v, want everything", u, got[u])
		}
	}
	if n := len(f.saved()); n != 2 {
		t.Errorf("events of General and open offices stay in the event log: %d", n)
	}
}

// A private chat thread already names its audience: it is kept as is (no office members added, no activity).
func TestCallerAudienceIsKept(t *testing.T) {
	f := newFix(t)
	f.install(nil)
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvChatMessage, AgentID: "legal", Audience: []string{"u-eve"}, Text: "x",
		Payload: map[string]any{"from": "legal", "text": "hola"}})
	got := f.got()
	if len(got["u-eve"]) != 1 || len(got["u-pri"]) != 0 || len(got["u-boss"]) != 0 {
		t.Errorf("the caller's audience must be kept as is: %v", got)
	}
	if items, _ := f.store.ListActivity(f.ctx, f.org, "", 10); len(items) != 0 {
		t.Errorf("a private chat event logs no activity: %+v", items)
	}
}

// A private office nobody is in yet: its events reach nobody (never the whole organization).
func TestPrivateOfficeWithoutMembersReachesNobody(t *testing.T) {
	f := newFix(t)
	osvc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-empty", Name: "Vacía"}}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"legal": "d-empty"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "legal"}}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	if _, err := osvc.Enable(f.ctx, f.org, offices.Viewer{UserID: "u-boss", Admin: true}, "d-empty", offices.Private); err != nil {
		t.Fatal(err)
	}
	f.rec.SetAudienceFor(officeaudience.New(osvc, f.store, nil, quiet))
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "legal", Text: "empieza", Payload: map[string]any{"task": domain.Task{ID: "t-x", AgentID: "legal"}}})
	if got := f.got(); len(got) != 0 {
		t.Errorf("nobody may receive it: %v", got)
	}
}

// planRuntime runs every task at once and writes a one-line report.
type planRuntime struct{}

func (planRuntime) Plan(context.Context, application.PlanRequest) (application.PlanResponse, error) {
	return application.PlanResponse{}, nil
}
func (planRuntime) RunTask(context.Context, application.RunTaskRequest) (application.RunTaskResponse, error) {
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "hecho", Confidence: 0.9}}, nil
}
func (planRuntime) Consult(context.Context, application.ConsultRequest) (application.ConsultResponse, error) {
	return application.ConsultResponse{Answer: "x"}, nil
}
func (planRuntime) Synthesize(context.Context, application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	return application.SynthesizeResponse{Title: "T", Summary: "S", Sections: []domain.Section{}}, nil
}
func (planRuntime) Health(context.Context) (string, error) { return "simulation", nil }

// A whole request of a private agent, through the real orchestrator: whoever asked for it (not a member) receives
// it live from the first event, request.received and their own message included; the member too; nobody else. An
// event outside the run (no person in its context) still reaches them through the run meta (RequestedBy).
func TestRequesterReceivesTheirRequestLive(t *testing.T) {
	f := newFix(t)
	orch := f.install(planRuntime{})
	ctx := application.WithActor(f.ctx, "u-eve")
	id, err := orch.SubmitPlan(ctx, "revisa el contrato secreto", application.PlanResponse{
		Tasks: []application.PlannedTask{{Key: "a", Title: "Contrato", AgentID: "legal", DependsOn: []string{}}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := f.store.GetRequest(f.ctx, f.org, id)
		if err == nil && (r.Status == domain.RequestDone || r.Status == domain.RequestFailed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the request did not finish: %+v %v", r, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	orch.Wait()
	got := f.got()
	for _, u := range []string{"u-eve", "u-pri"} {
		for _, typ := range []string{domain.EvRequestReceived, domain.EvMessageSent, domain.EvTaskCompleted, domain.EvReportCreated} {
			if !slices.Contains(got[u], typ) {
				t.Errorf("%s must receive %s: %v", u, typ, got[u])
			}
		}
	}
	for _, u := range []string{"u-boss", "u-ana"} {
		for _, typ := range got[u] {
			if typ != domain.EvMetricsUpdated && typ != domain.EvAgentCreated {
				t.Errorf("%s (not a member) received %s: %v", u, typ, got[u])
			}
		}
	}
	tasks, _ := f.store.ListTasksByRequest(f.ctx, f.org, id)
	if len(tasks) != 1 {
		t.Fatalf("tasks: %+v", tasks)
	}
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskReviewed, AgentID: "legal", Payload: map[string]any{"task_id": tasks[0].ID}})
	if got := f.got(); !slices.Contains(got["u-eve"], domain.EvTaskReviewed) || len(got["u-boss"]) != 0 {
		t.Errorf("an event outside the run reaches the requester through the run meta: %v", got)
	}
}

// A connection of a private office (its office_id in the payload) is the office's: its live events reach its members
// only, with no activity line filed under any agent; a General connection keeps reaching everybody.
func TestPrivateOfficeConnectionEventsReachOnlyMembers(t *testing.T) {
	f := newFix(t)
	f.install(nil)
	f.rec.Emit(f.ctx, application.Action{Type: "connection.created",
		Payload: map[string]any{"connection": map[string]any{"id": "cn-1", "label": "Banco Privado", "office_id": "d-priv"}}})
	got := f.got()
	if !slices.Contains(got["u-pri"], "connection.created") {
		t.Errorf("the member must receive it: %v", got)
	}
	for _, u := range []string{"u-boss", "u-ana", "u-eve"} {
		if len(got[u]) != 0 {
			t.Errorf("%s (not a member) received %v", u, got[u])
		}
	}
	for _, ev := range f.saved() {
		if ev.Type == "connection.created" {
			t.Errorf("a private office connection event must stay out of the organization's event log: %+v", ev)
		}
	}

	f.rec.Emit(f.ctx, application.Action{Type: "connection.updated",
		Payload: map[string]any{"connection": map[string]any{"id": "cn-2", "label": "Correo", "office_id": "general"}}})
	got = f.got()
	for _, u := range []string{"u-boss", "u-ana", "u-pri", "u-eve"} {
		if !slices.Contains(got[u], "connection.updated") {
			t.Errorf("%s must receive a General connection's event: %v", u, got[u])
		}
	}
}
