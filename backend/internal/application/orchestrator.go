package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

const assistantID = "assistant"

var errBudget = errors.New("presupuesto de la organización agotado")

// Orchestrator turns a user request into a plan, executes it with the
// scheduler, handles consults and approvals, and produces a report.
type Orchestrator struct {
	cfg       Config
	store     Store
	rt        Runtime
	locks     Locker
	rec       *Recorder
	approvals *Approvals
	q         *Queries
	log       *slog.Logger

	style  StyleProvider  // optional: locale/regional tone per organization and agent
	budget *Budget        // hard caps: reserve before each runtime call, reconcile after
	conn   connState      // optional: controls guard, Tool Gateway, plan review (connections_flow.go)
	policy *PolicyService // optional: organization rules and the policy engine (policyflow.go)

	chatIdem idemCache // Idempotency-Key of POST /messages (chat.go)
	chatQ    chatQueue // per-conversation FIFO of chat turns (chat.go)

	mu     sync.Mutex      // guards base/cancel and serializes Reset vs. start
	root   context.Context // process lifetime
	base   context.Context // parent of in-flight runs; cancelled by Reset
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewOrchestrator(ctx context.Context, cfg Config, store Store, rt Runtime, locks Locker, rec *Recorder, approvals *Approvals, q *Queries, log *slog.Logger) *Orchestrator {
	o := &Orchestrator{cfg: cfg, store: store, rt: rt, locks: locks, rec: rec, approvals: approvals, q: q, log: log}
	o.budget = NewBudget(cfg, store, rec, log)
	o.root = ctx
	o.base, o.cancel = context.WithCancel(ctx)
	return o
}

// Wait blocks until all in-flight requests finished (used on shutdown and tests).
func (o *Orchestrator) Wait() { o.wg.Wait() }

// run is the in-memory state of one request execution.
type run struct {
	req    domain.Request
	convID string
	agents map[string]domain.Agent

	mu       sync.Mutex
	done     map[string]domain.Task // finished tasks by id
	involved map[string]bool

	preset *PlanResponse // plan from a workflow template: skips the runtime planner
	style  RunStyle      // locale and regional tone captured when the run started
	// requestedBy is the human who submitted the request ("" without auth):
	// the "on behalf of" of every tool request and the requester of its approvals.
	requestedBy string
	ext         runExt    // taint, read-only request flag (connections_flow.go)
	chat        *chatLink // set when the request was born in a chat turn (chat.go)
}

func (r *run) touch(agentID string) {
	r.mu.Lock()
	r.involved[agentID] = true
	r.mu.Unlock()
}

// Submit registers a request and starts processing it asynchronously.
func (o *Orchestrator) Submit(ctx context.Context, text string) (string, error) {
	return o.submit(ctx, text, nil)
}

// SubmitPlan registers a request whose plan is already known (a workflow
// template instantiated with parameters). It skips the runtime planner but
// follows exactly the same path as Submit afterwards: dependency validation,
// parallel scheduling, approvals, report.
func (o *Orchestrator) SubmitPlan(ctx context.Context, text string, plan PlanResponse) (string, error) {
	if len(plan.Tasks) == 0 {
		return "", fmt.Errorf("%w: plan without tasks", domain.ErrInvalid)
	}
	return o.submit(ctx, text, &plan)
}

func (o *Orchestrator) submit(ctx context.Context, text string, preset *PlanResponse) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w: text is required", domain.ErrInvalid)
	}
	agents, err := o.store.ListAgents(ctx, o.org(ctx))
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	req := domain.Request{ID: newID(), Text: text, Status: domain.RequestPlanning, CreatedAt: now}
	if err := o.store.CreateRequest(ctx, o.org(ctx), req); err != nil {
		return "", err
	}
	o.initRequestCap(ctx, req.ID)
	conv := domain.Conversation{ID: newID(), Title: truncate(text, 80), Participants: []string{"user", assistantID}, RequestID: &req.ID, LastMessageAt: now}
	if err := o.store.CreateConversation(ctx, o.org(ctx), conv); err != nil {
		return "", err
	}
	rs := &run{req: req, convID: conv.ID, agents: map[string]domain.Agent{}, done: map[string]domain.Task{}, involved: map[string]bool{assistantID: true}}
	for _, a := range agents {
		rs.agents[a.ID] = a
	}
	rs.preset = preset
	rs.requestedBy = ActorFrom(ctx, "")
	rs.style = o.loadStyle(ctx)
	rs.chat = chatLinkFrom(ctx)
	o.rec.Emit(ctx, Action{Type: domain.EvRequestReceived, Entity: "request", EntityID: req.ID,
		Payload: map[string]any{"request_id": req.ID, "text": text}, Text: "Nueva solicitud: " + truncate(text, 100)})
	o.sendMessage(ctx, rs, "user", assistantID, "chat", text, nil)
	o.emitMetrics(ctx)

	o.mu.Lock()
	defer o.mu.Unlock()
	// The background run keeps the lifetime of the orchestrator but carries the
	// tenant of the request that started it.
	rctx := WithRequestID(WithOrg(o.base, o.org(ctx)), req.ID)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.process(rctx, rs)
	}()
	return req.ID, nil
}

func (o *Orchestrator) process(ctx context.Context, rs *run) {
	defer o.releaseAgents(ctx, rs)
	o.setState(ctx, assistantID, domain.StateThinking, "Analizando la solicitud", nil, 10)

	var plan PlanResponse
	var err error
	if rs.preset != nil {
		plan = *rs.preset
	} else {
		err = o.call(ctx, "plan", func(c context.Context) (err error) {
			agents := make([]PlanAgent, 0, len(rs.agents))
			for _, a := range sortedAgents(rs.agents) {
				agents = append(agents, NewPlanAgent(a, rs.style.Locale))
			}
			plan, err = o.rt.Plan(c, PlanRequest{RequestText: rs.req.Text, Agents: agents, BudgetUSD: o.cfg.BudgetUSD,
				Locale: rs.style.Locale, Tone: rs.style.Tone})
			return err
		})
	}
	if err != nil {
		o.failRequest(ctx, rs, assistantID, "No pude planificar la solicitud", err)
		return
	}
	rs.req.Plan = domain.Plan{Objectives: plan.Objectives, ClarifyingQuestions: plan.ClarifyingQuestions}
	if len(plan.Tasks) == 0 {
		if len(plan.ClarifyingQuestions) > 0 {
			o.sendMessage(ctx, rs, assistantID, "user", "chat", "Necesito aclarar antes de continuar:\n- "+strings.Join(plan.ClarifyingQuestions, "\n- "), nil)
		}
		o.failRequest(ctx, rs, assistantID, "Solicitud sin tareas ejecutables", errors.New("el plan no contiene tareas"))
		return
	}
	tasks, err := o.createTasks(ctx, rs, plan)
	if err != nil {
		o.failRequest(ctx, rs, assistantID, "Plan inválido", err)
		return
	}
	if !o.reviewPlan(ctx, rs, tasks) {
		return // plan rejected or not reviewed: the request is already failed with an explanation
	}
	if !o.confirmEstimate(ctx, rs, tasks) {
		return // cancelled or not confirmed: the request is already failed with an explanation
	}
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	o.setState(ctx, assistantID, domain.StateWaiting, "Coordinando al equipo", nil, 30)

	byID := make(map[string]domain.Task, len(tasks))
	nodes := make([]Node, 0, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
		nodes = append(nodes, Node{ID: t.ID, DependsOn: t.DependsOn})
	}
	sched := Scheduler{MaxParallel: o.cfg.MaxParallel}
	outcomes := sched.Run(ctx, nodes,
		func(c context.Context, id string) Outcome { return o.runTask(c, rs, byID[id]) },
		func(id string) { o.skipTask(ctx, rs, byID[id]) })
	if ctx.Err() != nil {
		return // demo reset or shutdown
	}
	o.finish(ctx, rs, tasks, outcomes)
}

// finish synthesizes the report from completed tasks.
func (o *Orchestrator) finish(ctx context.Context, rs *run, tasks []domain.Task, outcomes map[string]Outcome) {
	var outs []SynthOutput
	var unfinished []string
	contributors := []string{}
	for _, t := range tasks {
		if outcomes[t.ID] == OutcomeDone {
			d := rs.done[t.ID]
			outs = append(outs, SynthOutput{TaskID: t.ID, AgentID: t.AgentID, Title: t.Title, Output: *d.Output})
			if !slices.Contains(contributors, t.AgentID) {
				contributors = append(contributors, t.AgentID)
			}
		} else {
			unfinished = append(unfinished, fmt.Sprintf("%s (%s)", t.Title, statusLabel(outcomes[t.ID])))
		}
	}
	if len(outs) == 0 {
		o.failRequest(ctx, rs, assistantID, "Ninguna tarea se completó", errors.New("sin resultados para consolidar"))
		return
	}
	o.setState(ctx, assistantID, domain.StateWorking, "Consolidando resultados", nil, 85)
	var syn SynthesizeResponse
	synRes, err := o.reserveOrPause(ctx, rs, assistantID, "", domain.UsageSynthesize)
	if err != nil {
		if ctx.Err() == nil {
			o.failRequest(ctx, rs, assistantID, "No pude consolidar el informe", err)
		}
		return
	}
	err = o.call(ctx, "synthesize", func(c context.Context) (err error) {
		syn, err = o.rt.Synthesize(c, SynthesizeRequest{RequestText: rs.req.Text, Outputs: outs,
			Locale: rs.style.Locale, Tone: rs.style.Tone})
		return err
	})
	if err != nil {
		synRes.Release()
		o.failRequest(ctx, rs, assistantID, "No pude consolidar el informe", err)
		return
	}
	o.recordUsage(ctx, rs, "", assistantID, domain.UsageSynthesize, syn.Usage, nil, synRes)
	if len(unfinished) > 0 {
		syn.Sections = append(syn.Sections, domain.Section{Heading: "Tareas no completadas", Body: "- " + strings.Join(unfinished, "\n- ")})
	}
	if syn.Sections == nil {
		syn.Sections = []domain.Section{}
	}
	if !slices.Contains(contributors, assistantID) {
		contributors = append(contributors, assistantID)
	}
	cur, err := o.store.GetRequest(ctx, o.org(ctx), rs.req.ID)
	if err != nil {
		o.failRequest(ctx, rs, assistantID, "No pude leer la solicitud", err)
		return
	}
	report := domain.Report{ID: newID(), RequestID: rs.req.ID, Title: syn.Title, Summary: syn.Summary, Sections: syn.Sections,
		Contributors: contributors, CostUSD: cur.CostUSD, CreatedAt: time.Now().UTC()}
	if err := o.store.CreateReport(ctx, o.org(ctx), report); err != nil {
		o.failRequest(ctx, rs, assistantID, "No pude guardar el informe", err)
		return
	}
	rs.req.ReportID = &report.ID
	o.setRequestStatus(ctx, rs, domain.RequestDone)
	o.rec.Emit(ctx, Action{Type: domain.EvReportCreated, AgentID: assistantID, Entity: "report", EntityID: report.ID,
		Payload: map[string]any{"report": report}, Text: "Informe listo: " + report.Title})
	o.setState(ctx, assistantID, domain.StateCompleted, "Informe entregado", nil, 100)
	o.sendMessage(ctx, rs, assistantID, "user", "chat", "El informe está listo: "+report.Title, nil)
	o.chatEchoReport(ctx, rs, report.Title)
	o.rec.Emit(ctx, Action{Type: domain.EvRequestCompleted, Entity: "request", EntityID: rs.req.ID,
		Payload: map[string]any{"request_id": rs.req.ID, "report_id": report.ID}, Text: "Solicitud completada"})
	o.emitMetrics(ctx)
}

func statusLabel(o Outcome) string {
	switch o {
	case OutcomeFailed:
		return "fallida"
	case OutcomeBlocked:
		return "bloqueada"
	}
	return "no ejecutada"
}

// createTasks validates the plan (keys, agents, cycles, depth) and persists tasks.
func (o *Orchestrator) createTasks(ctx context.Context, rs *run, plan PlanResponse) ([]domain.Task, error) {
	ids := map[string]string{}
	byKey := map[string]PlannedTask{}
	for i := range plan.Tasks {
		pt := &plan.Tasks[i]
		if pt.Key == "" {
			pt.Key = fmt.Sprintf("t%d", i+1)
		}
		if _, dup := byKey[pt.Key]; dup {
			return nil, fmt.Errorf("clave de tarea duplicada: %s", pt.Key)
		}
		byKey[pt.Key] = *pt
		ids[pt.Key] = newID()
	}
	depth := map[string]int{}
	visiting := map[string]bool{}
	var dfs func(key string) (int, error)
	dfs = func(key string) (int, error) {
		if d, ok := depth[key]; ok {
			return d, nil
		}
		if visiting[key] {
			return 0, fmt.Errorf("dependencia circular en %s", key)
		}
		visiting[key] = true
		d := 1
		for _, dep := range byKey[key].DependsOn {
			if _, ok := byKey[dep]; !ok {
				return 0, fmt.Errorf("la tarea %s depende de una clave desconocida: %s", key, dep)
			}
			dd, err := dfs(dep)
			if err != nil {
				return 0, err
			}
			d = max(d, dd+1)
		}
		visiting[key] = false
		depth[key] = d
		return d, nil
	}
	for _, pt := range plan.Tasks {
		d, err := dfs(pt.Key)
		if err != nil {
			return nil, err
		}
		if maxDepth := max(o.cfg.MaxDepth, plan.MaxDepth); d > maxDepth { // plan.MaxDepth: long project chains (projects_hooks.go)
			o.rec.Audit(ctx, domain.AuditLog{Actor: assistantID, Action: "delegation.depth_exceeded", Entity: "request", EntityID: rs.req.ID,
				Details: map[string]any{"task": pt.Key, "depth": d, "max": maxDepth}})
			return nil, fmt.Errorf("profundidad de delegación %d supera el máximo %d", d, maxDepth)
		}
	}

	now := time.Now().UTC()
	tasks := make([]domain.Task, 0, len(plan.Tasks))
	for _, pt := range plan.Tasks {
		agent := pt.AgentID
		if _, ok := rs.agents[agent]; !ok {
			o.rec.Audit(ctx, domain.AuditLog{Actor: assistantID, Action: "plan.agent_reassigned", Entity: "request", EntityID: rs.req.ID,
				Details: map[string]any{"task": pt.Key, "unknown_agent": agent}})
			agent = assistantID
		}
		deps := make([]string, 0, len(pt.DependsOn))
		for _, k := range pt.DependsOn {
			deps = append(deps, ids[k])
		}
		reason := cleanReason(pt.Reason)
		if a, why := o.chatReassign(ctx, rs, agent, pt); why != "" { // the addressed agent declined it: the topic owner takes it
			agent, reason = a, why
		}
		if reason == "" {
			reason = defaultAssignReason(rs.style.Locale, rs.agents[agent])
		}
		t := domain.Task{ID: ids[pt.Key], RequestID: rs.req.ID, Title: pt.Title, Description: pt.Description, AgentID: agent,
			Status: domain.TaskPending, DependsOn: deps, CreatedAt: now, Depth: depth[pt.Key], AssignedReason: reason}
		if err := o.store.CreateTask(ctx, o.org(ctx), t); err != nil {
			return nil, err
		}
		rs.touch(agent)
		tasks = append(tasks, t)
	}
	if err := o.store.UpdateRequest(ctx, o.org(ctx), rs.req); err != nil {
		return nil, err
	}
	o.rec.Emit(ctx, Action{Type: domain.EvPlanCreated, AgentID: assistantID, Entity: "request", EntityID: rs.req.ID,
		Payload: map[string]any{"request_id": rs.req.ID, "tasks": planTasks(tasks)},
		Text:    fmt.Sprintf("Plan creado con %d tareas", len(tasks))})
	for _, t := range tasks {
		o.rec.Emit(ctx, Action{Type: domain.EvTaskCreated, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": t, "assigned_reason": t.AssignedReason}})
		if t.AgentID != assistantID {
			tid := t.ID
			o.sendMessage(ctx, rs, assistantID, t.AgentID, "delegation", "Te asigno: "+t.Title, &tid)
		}
	}
	o.announceAssignments(ctx, rs, tasks) // chat-born requests: who got what and why, in plain language
	return tasks, nil
}

// runTask executes one task end to end. It is called by the scheduler.
func (o *Orchestrator) runTask(ctx context.Context, rs *run, t domain.Task) Outcome {
	// Lock per task so that a second instance never runs it twice.
	unlock, ok, err := o.locks.Lock(ctx, "task:"+t.ID, o.cfg.LockTTL)
	if err != nil {
		o.log.Warn("task lock unavailable, continuing without it", "task", t.ID, "err", err)
	} else if !ok {
		o.failTask(ctx, rs, t, errors.New("la tarea ya está siendo ejecutada por otro worker"))
		return OutcomeFailed
	} else {
		defer unlock()
	}

	if !o.admitTask(ctx, rs, t) {
		return OutcomeFailed // kill switch / pause never released before shutdown
	}
	if out, handled := o.projectGate(ctx, rs, t); handled {
		return out // project paused/cancelled, human gate or milestone (projects_hooks.go)
	}
	agent := rs.agents[t.AgentID]
	// Hold budget for the call first; a request/agent cap pauses here (visibly) until raised.
	res, err := o.reserveOrPause(ctx, rs, t.AgentID, t.ID, domain.UsageRunTask)
	if err != nil {
		if ctx.Err() != nil {
			return OutcomeFailed
		}
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed
	}
	now := time.Now().UTC()
	t.Status, t.StartedAt = domain.TaskRunning, &now
	if err := o.store.UpdateTask(ctx, o.org(ctx), t); err != nil {
		res.Release()
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed
	}
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWorking, t.Title, &tid, 10)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskStarted, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: fmt.Sprintf("%s empezó: %s", agent.Name, t.Title)})
	o.emitMetrics(ctx)

	in := o.buildRunRequest(ctx, rs, t, agent)
	var resp RunTaskResponse
	err = o.call(ctx, "run-task", func(c context.Context) (err error) {
		resp, err = o.rt.RunTask(c, in)
		return err
	})
	if err != nil {
		res.Release()
		if ctx.Err() != nil {
			return OutcomeFailed
		}
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed
	}
	o.recordUsage(ctx, rs, t.ID, t.AgentID, domain.UsageRunTask, resp.Usage, resp.ToolRequests, res)
	resp.Output.Normalize()
	resp = o.connectionReads(ctx, rs, &t, agent, in, resp)
	resp.Output.Normalize()
	t.Output = &resp.Output
	o.setState(ctx, t.AgentID, domain.StateWorking, "Revisando resultados: "+t.Title, &tid, 60)

	o.handleConsults(ctx, rs, &t, agent, resp.Consults)
	if out := o.handleTools(ctx, rs, &t, agent, resp.ToolRequests); out != OutcomeDone {
		return out
	}

	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskDone, &fin
	if err := o.store.UpdateTask(ctx, o.org(ctx), t); err != nil {
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed
	}
	rs.mu.Lock()
	rs.done[t.ID] = t
	rs.mu.Unlock()
	if err := o.store.SetMemory(ctx, o.org(ctx), t.AgentID, domain.Memory{Scope: "agent", Key: "last_task", Value: t.Title + ": " + t.Output.Summary}); err != nil {
		o.log.Warn("set memory", "err", err)
	}
	o.setState(ctx, t.AgentID, domain.StateCompleted, "Completó: "+t.Title, &tid, 100)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskCompleted, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: fmt.Sprintf("%s completó: %s", agent.Name, t.Title)})
	o.emitMetrics(ctx)
	return OutcomeDone
}

func (o *Orchestrator) buildRunRequest(ctx context.Context, rs *run, t domain.Task, agent domain.Agent) RunTaskRequest {
	rc := RunContext{RequestText: rs.req.Text, DependencyOutputs: []DependencyOutput{}, Memory: []MemoryEntry{}}
	rs.mu.Lock()
	for _, d := range t.DependsOn {
		if dt, ok := rs.done[d]; ok && dt.Output != nil {
			rc.DependencyOutputs = append(rc.DependencyOutputs, DependencyOutput{TaskID: dt.ID, AgentID: dt.AgentID, Output: *dt.Output})
		}
	}
	rs.mu.Unlock()
	mem, err := o.store.ListMemory(ctx, o.org(ctx), t.AgentID)
	if err != nil {
		o.log.Warn("list memory", "err", err)
	}
	for _, m := range mem {
		rc.Memory = append(rc.Memory, MemoryEntry{Scope: m.Scope, Key: m.Key, Value: m.Value})
	}
	return RunTaskRequest{
		Task:    RunTaskInfo{ID: t.ID, Title: t.Title, Description: t.Description, AgentID: t.AgentID},
		Agent:   RunAgentInfo{ID: agent.ID, Role: agent.Role, Title: agent.Title, Persona: agent.Persona, Responsibilities: agent.Responsibilities, Tools: agent.Tools, Area: areaOf(agent.Role, rs.style.Locale)},
		Context: rc,
		Locale:  rs.style.Locale,
		Tone:    rs.style.ToneFor(t.AgentID),
	}
}

// handleConsults performs agent-to-agent consultations requested by a task.
func (o *Orchestrator) handleConsults(ctx context.Context, rs *run, t *domain.Task, from domain.Agent, consults []ConsultRequestItem) {
	for _, c := range consults {
		to, ok := rs.agents[c.ToAgentID]
		if !ok || to.ID == from.ID {
			o.rec.Audit(ctx, domain.AuditLog{Actor: from.ID, Action: "consult.rejected", Entity: "task", EntityID: t.ID, Details: map[string]any{"to": c.ToAgentID}})
			continue
		}
		if t.Depth+1 > o.cfg.MaxDepth {
			o.rec.Audit(ctx, domain.AuditLog{Actor: from.ID, Action: "delegation.depth_exceeded", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"to": to.ID, "depth": t.Depth + 1, "max": o.cfg.MaxDepth}})
			continue
		}
		rs.touch(to.ID)
		tid := t.ID
		prev, _ := o.store.GetAgent(ctx, o.org(ctx), to.ID)
		o.sendMessage(ctx, rs, from.ID, to.ID, "consult", c.Question, &tid)
		o.setState(ctx, from.ID, domain.StateTalking, "Consultando a "+to.Name, &tid, 65)
		o.setState(ctx, to.ID, domain.StateTalking, "Respondiendo a "+from.Name, prev.CurrentTaskID, prev.Progress)

		var ans ConsultResponse
		cres, err := o.reserveOrPause(ctx, rs, to.ID, t.ID, domain.UsageConsult)
		if err == nil {
			err = o.call(ctx, "consult", func(cc context.Context) (err error) {
				ans, err = o.rt.Consult(cc, ConsultRequest{FromAgentID: from.ID, ToAgentID: to.ID, Question: c.Question,
					Locale: rs.style.Locale, Tone: rs.style.ToneFor(to.ID),
					Context: fmt.Sprintf("Solicitud: %s\nTarea: %s\nResumen parcial: %s", rs.req.Text, t.Title, t.Output.Summary)})
				return err
			})
		}
		if err == nil {
			o.recordUsage(ctx, rs, t.ID, to.ID, domain.UsageConsult, ans.Usage, nil, cres)
			o.sendMessage(ctx, rs, to.ID, from.ID, "answer", ans.Answer, &tid)
			t.Output.Evidence = append(t.Output.Evidence, fmt.Sprintf("Consulta a %s: %s", to.Name, ans.Answer))
		} else if ctx.Err() == nil {
			cres.Release()
			o.reportError(ctx, to.ID, fmt.Sprintf("la consulta de %s a %s falló: %v", from.Name, to.Name, err))
		}
		o.setState(ctx, from.ID, domain.StateWorking, "Revisando resultados: "+t.Title, &tid, 70)
		restore := prev.State
		if restore == domain.StateTalking || restore == domain.StateCompleted {
			restore = domain.StateIdle
		}
		o.setState(ctx, to.ID, restore, prev.Activity, prev.CurrentTaskID, prev.Progress)
	}
}

// needsApproval is the baseline rule every organization gets (high runtime
// risk, the APPROVAL_ACTIONS list, suggest/approve_each autonomy). With a
// PolicyService installed it is its floor: the organization rules can only add
// to it.
func (o *Orchestrator) needsApproval(a domain.Agent, tr ToolRequest) bool {
	return strings.EqualFold(tr.Risk, "high") || o.cfg.ApprovalActions[tr.Action] ||
		a.Autonomy == "suggest" || a.Autonomy == "approve_each"
}

// SetPolicy installs the policy service (internal/policy engine plus the
// organization rules). Without it the baseline rule decides, as before.
func (o *Orchestrator) SetPolicy(p *PolicyService) { o.policy = p }

func (o *Orchestrator) routeOf(ctx context.Context, agent domain.Agent, tr ToolRequest) GatewayRoute {
	if o.conn.gw == nil {
		return RouteNone
	}
	return o.conn.gw.Route(ctx, o.org(ctx), agent.ID, tr.Tool, tr.Action)
}

func (o *Orchestrator) policyInput(rs *run, agent domain.Agent, tr ToolRequest) PolicyInput {
	return PolicyInput{Agent: agent, Tool: tr.Tool, Action: tr.Action, Args: tr.Args, Risk: tr.Risk, RequestedBy: rs.requestedBy}
}

// decideTool asks the policy layer about a tool request and audits the
// decision (metadata only: never the arguments).
func (o *Orchestrator) decideTool(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest) PolicyVerdict {
	if o.policy == nil {
		v := PolicyVerdict{Effect: "allow", RequiredApprovals: 1, Source: "baseline", Autonomy: agent.Autonomy}
		if o.needsApproval(agent, tr) {
			v.Effect, v.RuleID = "require_approval", "baseline"
		}
		return v
	}
	v := o.policy.Decide(ctx, o.policyInput(rs, agent, tr))
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "policy.decision", Entity: "task", EntityID: t.ID, Details: map[string]any{
		"tool": tr.Tool, "action": tr.Action, "risk": tr.Risk, "effect": v.Effect, "rule_id": v.RuleID, "reason": v.Reason, "source": v.Source,
		"trace": v.Trace, "required_role": v.RequiredRole, "required_approvals": v.RequiredApprovals, "autonomy": v.Autonomy,
		"args_hash": v.ArgsHash, "amount": v.Amount, "on_behalf_of": rs.requestedBy}})
	return v
}

// policyDenied records a denied tool request and tells the task about it.
func (o *Orchestrator) policyDenied(ctx context.Context, t *domain.Task, agent domain.Agent, tr ToolRequest, v PolicyVerdict, simulated bool) {
	d := map[string]any{"tool": tr.Tool, "action": tr.Action, "reason": "policy:" + v.RuleID}
	if simulated {
		d["simulated"] = true
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID, Details: d})
	if t.Output != nil {
		t.Output.Evidence = append(t.Output.Evidence, fmt.Sprintf("Acción %s.%s denegada por política (%s)", tr.Tool, tr.Action, v.RuleID))
	}
}

// governApproval copies the governance requirements of the verdict into the approval.
func governApproval(ap *domain.Approval, rs *run, v PolicyVerdict) {
	ap.RequiredApprovals, ap.RequiredRole, ap.NoSelfApproval, ap.PolicyRule = max(v.RequiredApprovals, 1), v.RequiredRole, v.NoSelfApproval, v.RuleID
	ap.RequestedBy = rs.requestedBy
}

func approverIDs(ap domain.Approval) []string {
	out := make([]string, 0, len(ap.Decisions))
	for _, d := range ap.Decisions {
		out = append(out, d.By)
	}
	return out
}

// handleTools applies the approval policy to the runtime's tool requests.
// Phase 1 has no real tools: approved/allowed actions are recorded as executed.
func (o *Orchestrator) handleTools(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, reqs []ToolRequest) Outcome {
	tid := t.ID
	for _, tr := range reqs {
		route := o.routeOf(ctx, agent, tr)
		if route == RouteRead {
			// Reads never need approval; the gateway skips the ones that survived the read rounds.
			if _, out := o.gatewayTool(ctx, rs, t, agent, tr, PolicyVerdict{}); out != OutcomeDone {
				return out
			}
			continue
		}
		if route == RouteNone && o.legacyBlocked(ctx, rs, t, agent, tr) {
			continue
		}
		pv := o.decideTool(ctx, rs, t, agent, tr)
		if pv.Denied() {
			o.policyDenied(ctx, t, agent, tr, pv, route == RouteNone)
			continue
		}
		if route != RouteNone {
			if _, out := o.gatewayTool(ctx, rs, t, agent, tr, pv); out != OutcomeDone {
				return out
			}
			continue
		}
		details, _ := json.Marshal(tr.Args)
		if !pv.NeedsApproval() {
			if o.policy != nil {
				if rv := o.policy.Reserve(ctx, o.policyInput(rs, agent, tr)); rv.Denied() {
					o.policyDenied(ctx, t, agent, tr, rv, true)
					continue
				}
			}
			o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "risk": tr.Risk, "arg_keys": argKeys(tr.Args), "simulated": true}})
			continue
		}
		risk := strings.ToLower(tr.Risk)
		if risk != "low" && risk != "medium" && risk != "high" {
			risk = "medium"
		}
		ap := domain.Approval{TaskID: t.ID, AgentID: agent.ID, Action: tr.Action, Risk: risk,
			Title:   fmt.Sprintf("%s: %s", strings.ReplaceAll(tr.Action, "_", " "), t.Title),
			Details: fmt.Sprintf("Herramienta %s · acción %s · args %s", tr.Tool, tr.Action, details)}
		governApproval(&ap, rs, pv)
		t.Status = domain.TaskAwaitingApproval
		if err := o.store.UpdateTask(ctx, o.org(ctx), *t); err != nil {
			o.failTask(ctx, rs, *t, err)
			return OutcomeFailed
		}
		ap, ch, err := o.approvals.Request(ctx, ap)
		if err != nil {
			o.failTask(ctx, rs, *t, err)
			return OutcomeFailed
		}
		o.setRequestStatus(ctx, rs, domain.RequestAwaitingApproval)
		o.setState(ctx, agent.ID, domain.StateAwaitingApproval, "Esperando aprobación: "+ap.Title, &tid, 80)
		o.emitMetrics(ctx)
		res, err := o.approvals.Wait(ctx, ch, ap.ID)
		if err != nil {
			return OutcomeFailed // cancelled
		}
		o.setRequestStatus(ctx, rs, domain.RequestRunning)
		approved := res.Status == domain.ApprovalApproved
		if approved && o.policy != nil {
			// The rules may have changed while the approval waited: re-validate.
			if rv := o.policy.Recheck(ctx, o.policyInput(rs, agent, tr), res.ID, approverIDs(res)); rv.Denied() {
				approved = false
				o.policyDenied(ctx, t, agent, tr, rv, true)
			}
		}
		if !approved {
			fin := time.Now().UTC()
			t.Status, t.FinishedAt = domain.TaskBlocked, &fin
			if err := o.store.UpdateTask(ctx, o.org(ctx), *t); err != nil {
				o.log.Warn("update task", "err", err)
			}
			o.setState(ctx, agent.ID, domain.StateBlocked, "Acción rechazada: "+ap.Title, &tid, 80)
			o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: agent.ID, Entity: "task", EntityID: t.ID,
				Payload: map[string]any{"task": *t}, Text: fmt.Sprintf("Tarea bloqueada (aprobación rechazada): %s", t.Title)})
			o.emitMetrics(ctx)
			return OutcomeBlocked
		}
		t.Status = domain.TaskRunning
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "arg_keys": argKeys(tr.Args), "approval_id": res.ID, "approvers": approverIDs(res), "simulated": true}})
		o.setState(ctx, agent.ID, domain.StateWorking, "Acción aprobada, finalizando: "+t.Title, &tid, 90)
	}
	return OutcomeDone
}

func (o *Orchestrator) failTask(ctx context.Context, rs *run, t domain.Task, cause error) {
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskFailed, &fin
	if err := o.store.UpdateTask(context.WithoutCancel(ctx), o.org(ctx), t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateError, "Error: "+truncate(cause.Error(), 120), &tid, 0)
	o.reportError(ctx, t.AgentID, fmt.Sprintf("la tarea %q falló: %v", t.Title, cause))
	o.rec.Emit(ctx, Action{Type: domain.EvTaskFailed, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: fmt.Sprintf("Tarea fallida: %s", t.Title)})
	o.emitMetrics(ctx)
}

func (o *Orchestrator) skipTask(ctx context.Context, rs *run, t domain.Task) {
	if ctx.Err() != nil {
		return
	}
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskBlocked, &fin
	if err := o.store.UpdateTask(ctx, o.org(ctx), t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: fmt.Sprintf("Tarea bloqueada por dependencias: %s", t.Title)})
	o.emitMetrics(ctx)
}

// failRequest marks the request failed and makes the failure visible.
func (o *Orchestrator) failRequest(ctx context.Context, rs *run, agentID, what string, cause error) {
	if ctx.Err() != nil {
		return
	}
	o.setRequestStatus(ctx, rs, domain.RequestFailed)
	o.setState(ctx, agentID, domain.StateError, what, nil, 0)
	o.reportError(ctx, agentID, fmt.Sprintf("%s: %v", what, cause))
	o.sendMessage(ctx, rs, "system", "user", "chat", fmt.Sprintf("%s: %v", what, cause), nil)
	o.chatEchoFailure(ctx, rs, what, cause)
	o.emitMetrics(ctx)
}

func (o *Orchestrator) reportError(ctx context.Context, agentID, msg string) {
	o.log.Warn("orchestrator error", "agent", agentID, "msg", msg)
	o.rec.Emit(ctx, Action{Type: domain.EvError, AgentID: agentID, Entity: "agent", EntityID: agentID,
		Payload: map[string]any{"agent_id": agentID, "message": msg}, Text: "Error: " + truncate(msg, 140)})
}

func (o *Orchestrator) setRequestStatus(ctx context.Context, rs *run, st domain.RequestStatus) {
	rs.mu.Lock()
	prev := rs.req.Status
	rs.req.Status = st
	r := rs.req
	rs.mu.Unlock()
	if err := o.store.UpdateRequest(context.WithoutCancel(ctx), o.org(ctx), r); err != nil {
		o.log.Warn("update request", "err", err)
	}
	if prev != st && (isCostGate(st) || isCostGate(prev)) {
		o.rec.Emit(ctx, Action{Type: domain.EvRequestStatus, Entity: "request", EntityID: r.ID, SkipAudit: true,
			Payload: map[string]any{"request_id": r.ID, "status": st}})
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "request.status." + string(st), Entity: "request", EntityID: r.ID})
}

func (o *Orchestrator) setState(ctx context.Context, agentID string, st domain.AgentState, activity string, taskID *string, progress int) {
	ctx = context.WithoutCancel(ctx)
	if err := o.store.UpdateAgentState(ctx, o.org(ctx), agentID, st, activity, taskID, progress); err != nil {
		o.log.Warn("update agent state", "agent", agentID, "err", err)
	}
	o.rec.Emit(ctx, Action{Type: domain.EvAgentState, AgentID: agentID, SkipAudit: true,
		Payload: map[string]any{"agent_id": agentID, "state": st, "activity": activity, "task_id": taskID, "progress": progress}})
}

// releaseAgents returns involved agents to idle once the request is over.
func (o *Orchestrator) releaseAgents(ctx context.Context, rs *run) {
	if o.cfg.IdleDelay > 0 {
		select {
		case <-time.After(o.cfg.IdleDelay):
		case <-ctx.Done():
		}
	}
	if ctx.Err() != nil {
		return
	}
	rs.mu.Lock()
	ids := make([]string, 0, len(rs.involved))
	for id := range rs.involved {
		ids = append(ids, id)
	}
	rs.mu.Unlock()
	for _, id := range ids {
		a, err := o.store.GetAgent(ctx, o.org(ctx), id)
		if err != nil || a.State == domain.StateError || a.State == domain.StateAwaitingApproval {
			continue
		}
		o.setState(ctx, id, domain.StateIdle, "Disponible", nil, 0)
	}
}

func (o *Orchestrator) emitMetrics(ctx context.Context) {
	m, err := o.q.Metrics(context.WithoutCancel(ctx))
	if err != nil {
		o.log.Warn("metrics", "err", err)
		return
	}
	o.rec.Emit(ctx, Action{Type: domain.EvMetricsUpdated, SkipAudit: true, Payload: map[string]any{"metrics": m}})
}

// sendMessage stores a message and emits message.sent.
func (o *Orchestrator) sendMessage(ctx context.Context, rs *run, from, to, kind, text string, taskID *string) {
	m := domain.Message{ID: newID(), ConversationID: rs.convID, From: from, To: to, Kind: kind, Text: text, TaskID: taskID, TS: time.Now().UTC()}
	if err := o.store.AddMessage(ctx, o.org(ctx), m); err != nil {
		o.log.Warn("add message", "err", err)
		return
	}
	act := Action{Type: domain.EvMessageSent, Entity: "message", EntityID: m.ID, Payload: map[string]any{"message": m}}
	if from != "user" && from != "system" {
		act.AgentID = from
	}
	if kind != "chat" {
		act.Text = fmt.Sprintf("%s → %s: %s", from, to, truncate(text, 100))
	}
	o.rec.Emit(ctx, act)
}

// call runs a runtime call with budget guard, per-attempt timeout and
// exponential backoff retries.
func (o *Orchestrator) call(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	attempts := max(1, o.cfg.MaxRetries)
	var err error
	for i := 0; i < attempts; i++ {
		if used, e := o.store.OrgCost(ctx, o.org(ctx)); e == nil && o.cfg.BudgetUSD > 0 && used >= o.cfg.BudgetUSD {
			o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "budget.exceeded", Entity: "org", EntityID: o.org(ctx),
				Details: map[string]any{"used_usd": used, "budget_usd": o.cfg.BudgetUSD, "call": name}})
			return errBudget
		}
		if i > 0 {
			select {
			case <-time.After(o.cfg.RetryBase << (i - 1)):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		cctx, cancel := context.WithTimeout(ctx, o.cfg.TaskTimeout)
		err = fn(cctx)
		cancel()
		if err == nil || ctx.Err() != nil {
			return err
		}
		o.log.Warn("runtime call failed", "call", name, "attempt", i+1, "of", attempts, "err", err)
		o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "runtime.call_failed", Entity: "runtime", EntityID: name,
			Details: map[string]any{"attempt": i + 1, "error": err.Error()}})
	}
	return fmt.Errorf("agent-runtime no disponible (%s): %w", name, err)
}

// PostUserMessage lets the user intervene in a conversation. The note is also
// stored in the memory of the conversation's agents so later tasks see it.
func (o *Orchestrator) PostUserMessage(ctx context.Context, convID, text string) (domain.Message, error) {
	text = SanitizeChatText(text)
	if text == "" {
		return domain.Message{}, fmt.Errorf("%w: text is required", domain.ErrInvalid)
	}
	if len([]rune(text)) > maxChatTextRunes {
		return domain.Message{}, fmt.Errorf("%w: text is too long (max %d characters)", domain.ErrInvalid, maxChatTextRunes)
	}
	conv, err := o.store.GetConversation(ctx, o.org(ctx), convID)
	if err != nil {
		return domain.Message{}, err
	}
	to := "all"
	if len(conv.Participants) == 2 {
		to = assistantID
	}
	m := domain.Message{ID: newID(), ConversationID: convID, From: "user", To: to, Kind: "chat", Text: text, TS: time.Now().UTC()}
	if err := o.store.AddMessage(ctx, o.org(ctx), m); err != nil {
		return m, err
	}
	for _, p := range conv.Participants {
		if p == "user" {
			continue
		}
		if err := o.store.SetMemory(ctx, o.org(ctx), p, domain.Memory{Scope: "conversation", Key: "user_note:" + m.ID, Value: text}); err != nil {
			o.log.Warn("set memory", "err", err)
		}
	}
	o.rec.Emit(ctx, Action{Type: domain.EvMessageSent, Entity: "message", EntityID: m.ID, Payload: map[string]any{"message": m},
		Text: "Intervención del usuario: " + truncate(text, 100)})
	return m, nil
}

// Reset cancels running work, clears execution data and reseeds.
func (o *Orchestrator) Reset(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cancel()
	o.wg.Wait()
	o.chatIdem.mu.Lock()
	o.chatIdem.m = nil // the conversations are about to be wiped: their keys must not replay
	o.chatIdem.mu.Unlock()
	parent := context.WithoutCancel(ctx)
	if err := o.store.Reset(parent, o.org(ctx)); err != nil {
		return err
	}
	if err := o.store.Seed(parent, domain.SeedOrg(o.cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		return err
	}
	o.base, o.cancel = context.WithCancel(o.root)
	o.budget.reset()
	o.rec.Audit(ctx, domain.AuditLog{Actor: "user", Action: "demo.reset", Entity: "org", EntityID: o.org(ctx)})
	agents, err := o.store.ListAgents(ctx, o.org(ctx))
	if err != nil {
		return err
	}
	for _, a := range agents {
		o.setState(ctx, a.ID, a.State, a.Activity, nil, 0)
	}
	o.emitMetrics(ctx)
	return nil
}

func sortedAgents(m map[string]domain.Agent) []domain.Agent {
	out := make([]domain.Agent, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b domain.Agent) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
