package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"aiworkforce/backend/internal/domain"
)

// This file wires cost control into the orchestrator: the pre-execution
// estimate with optional confirmation (improvement #1) and reserve / pause /
// resume around every runtime call (improvement #2).

type capCtxKey struct{}

// WithBudgetCap carries the budget_cap_usd of a new request from the API into
// Submit. A value <= 0 means "use the configured default".
func WithBudgetCap(ctx context.Context, capUSD float64) context.Context {
	return context.WithValue(ctx, capCtxKey{}, capUSD)
}

// Budget exposes the budget manager (used by the API).
func (o *Orchestrator) Budget() *Budget { return o.budget }

// initRequestCap stores the hard cap of a new request: the explicit one from
// the API, otherwise the configured default. No cap is stored when both are 0.
func (o *Orchestrator) initRequestCap(ctx context.Context, requestID string) {
	capUSD, _ := ctx.Value(capCtxKey{}).(float64)
	if capUSD <= 0 || math.IsNaN(capUSD) || math.IsInf(capUSD, 0) {
		capUSD = o.cfg.RequestBudgetCapUSD
	}
	if capUSD <= 0 {
		return
	}
	if err := o.store.SetBudgetCap(ctx, o.org(ctx), domain.ScopeRequest, requestID, capUSD); err != nil {
		o.log.Warn("set request cap", "err", err)
	}
}

func (o *Orchestrator) requestCap(ctx context.Context, requestID string) float64 {
	caps, err := o.store.ListBudgetCaps(ctx, o.org(ctx))
	if err != nil {
		return 0
	}
	return capsMap(caps)["request/"+requestID]
}

// ---- estimate ----

// buildEstimate asks the runtime for a range per task. Without /v1/estimate (or
// when it fails) it falls back to coarse backend constants, flagged with
// basis "fallback"; a fallback never forces a confirmation.
func (o *Orchestrator) buildEstimate(ctx context.Context, requestID, requestText string, tasks []domain.Task) domain.CostEstimate {
	in := EstimateRequest{RequestText: requestText}
	for _, t := range tasks {
		in.Tasks = append(in.Tasks, EstimateTaskIn{ID: t.ID, Title: t.Title, Description: t.Description, AgentID: t.AgentID, DependsOn: t.DependsOn})
	}
	est := domain.CostEstimate{RequestID: requestID, Currency: "USD", Tasks: []domain.TaskEstimate{}}
	var resp EstimateResponse
	ok := false
	if e, isEst := o.rt.(Estimator); isEst {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var err error
		resp, err = e.Estimate(cctx, in)
		cancel()
		if err != nil {
			o.log.Warn("runtime estimate failed, using fallback", "err", err)
		} else {
			ok = len(resp.Tasks) == len(tasks)
		}
	}
	if ok {
		est.Mode, est.Model, est.Basis = resp.Mode, resp.Model, resp.Basis
		if resp.Currency != "" {
			est.Currency = resp.Currency
		}
		for _, t := range resp.Tasks {
			est.Tasks = append(est.Tasks, domain.TaskEstimate{TaskID: t.ID, Title: t.Title, AgentID: t.AgentID, MinUSD: t.MinUSD, MaxUSD: t.MaxUSD})
		}
		est.Synthesis, est.Total = resp.Synthesis, resp.Total
	} else {
		est.Basis, est.Model = domain.BasisFallback, ""
		for _, t := range tasks {
			lo, hi := fallbackTaskRange(est.Model, len(t.DependsOn))
			est.Tasks = append(est.Tasks, domain.TaskEstimate{TaskID: t.ID, Title: t.Title, AgentID: t.AgentID, MinUSD: lo, MaxUSD: hi})
			est.Total.MinUSD += lo
			est.Total.MaxUSD += hi
		}
		est.Synthesis = domain.CostRange{MinUSD: recalcCost("", fallbackInMin, fallbackOutMin), MaxUSD: recalcCost("", fallbackInMax+fallbackInPerDep*len(tasks), fallbackOutMax)}
		est.Total.MinUSD += est.Synthesis.MinUSD
		est.Total.MaxUSD += est.Synthesis.MaxUSD
	}
	est.ThresholdUSD = o.cfg.ConfirmThresholdUSD
	est.BudgetCapUSD = o.requestCap(ctx, requestID)
	if est.Basis != domain.BasisFallback {
		switch {
		case est.BudgetCapUSD > 0 && est.Total.MaxUSD > est.BudgetCapUSD:
			est.RequiresConfirmation, est.ConfirmReason = true, domain.ConfirmReasonCap
		case est.ThresholdUSD > 0 && est.Total.MaxUSD > est.ThresholdUSD:
			est.RequiresConfirmation, est.ConfirmReason = true, domain.ConfirmReasonThreshold
		}
	}
	return est
}

// EstimateFor returns the stored estimate of a request, or recomputes it from
// the persisted tasks (e.g. after a restart).
func (o *Orchestrator) EstimateFor(ctx context.Context, requestID string) (domain.CostEstimate, error) {
	if e, ok := o.budget.Estimate(ctx, requestID); ok {
		return e, nil
	}
	req, err := o.store.GetRequest(ctx, o.org(ctx), requestID)
	if err != nil {
		return domain.CostEstimate{}, err
	}
	tasks, err := o.store.ListTasksByRequest(ctx, o.org(ctx), requestID)
	if err != nil {
		return domain.CostEstimate{}, err
	}
	if len(tasks) == 0 {
		return domain.CostEstimate{}, domain.ErrNotFound
	}
	return o.buildEstimate(ctx, requestID, req.Text, tasks), nil
}

// confirmEstimate computes and publishes the estimate and, when it exceeds a
// threshold or the request cap, waits for the user's decision. It returns
// false when the request must not continue (cancelled, timed out or aborted).
func (o *Orchestrator) confirmEstimate(ctx context.Context, rs *run, tasks []domain.Task) bool {
	return o.confirmEstimateFrom(ctx, rs, tasks, nil)
}

// confirmEstimateFrom is confirmEstimate; resumed (non-nil) is the cost
// confirmation a request was waiting on before a restart: the same estimate is
// asked again, with the original deadline (decision D-A1b), and it is asked
// even if a fresh estimate would not need it (the human never confirmed it).
func (o *Orchestrator) confirmEstimateFrom(ctx context.Context, rs *run, tasks []domain.Task, resumed *RunGate) bool {
	org := o.org(ctx)
	since := time.Now().UTC()
	var est domain.CostEstimate
	switch {
	case resumed != nil && resumed.Estimate != nil:
		est, since = *resumed.Estimate, resumed.StartedAt
	case resumed != nil:
		est, since = o.buildEstimate(ctx, rs.req.ID, rs.req.Text, tasks), resumed.StartedAt
	default:
		est = o.buildEstimate(ctx, rs.req.ID, rs.req.Text, tasks)
	}
	if resumed != nil && !est.RequiresConfirmation {
		est.RequiresConfirmation = true
		if est.ConfirmReason == "" {
			est.ConfirmReason = domain.ConfirmReasonThreshold
		}
	}
	o.budget.setEstimate(org, est)
	var ch chan Confirmation
	if est.RequiresConfirmation {
		ch = o.budget.awaitConfirmation(org, rs.req.ID) // register before publishing
		defer o.budget.dropConfirmation(org, rs.req.ID)
		saved := est
		o.setGate(ctx, rs, &RunGate{Kind: GateCostConfirmation, StartedAt: since, Estimate: &saved}) // durable: survives a restart
	}
	o.rec.Emit(ctx, Action{Type: domain.EvCostEstimated, AgentID: assistantID, Entity: "request", EntityID: rs.req.ID,
		Payload: map[string]any{"request_id": rs.req.ID, "estimate": est},
		Text:    fmt.Sprintf("Costo estimado: entre $%.2f y $%.2f", est.Total.MinUSD, est.Total.MaxUSD)})
	if ch == nil {
		return true
	}
	o.setRequestStatus(ctx, rs, domain.RequestAwaitingConfirmation)
	o.setState(ctx, assistantID, domain.StateWaiting, "Esperando tu confirmación del costo estimado", nil, 20)
	o.emitMetrics(ctx)
	timer := time.NewTimer(max(time.Until(since.Add(o.cfg.ApprovalTimeout)), 0))
	defer timer.Stop()
	select {
	case c := <-ch:
		if !c.Proceed {
			o.cancelPlanned(ctx, rs, tasks, "cancelada por el usuario")
			o.failRequest(ctx, rs, assistantID, "Solicitud cancelada", errors.New("no confirmaste la estimación de costo"))
			return false
		}
		o.setGate(ctx, rs, nil)
		return true
	case <-timer.C:
		o.cancelPlanned(ctx, rs, tasks, "sin confirmación de costo")
		o.failRequest(ctx, rs, assistantID, "Solicitud sin confirmar", errors.New("la estimación de costo no se confirmó a tiempo"))
		return false
	case <-ctx.Done():
		return false
	}
}

// cancelPlanned blocks the tasks of a request that will never start.
func (o *Orchestrator) cancelPlanned(ctx context.Context, rs *run, tasks []domain.Task, why string) {
	if ctx.Err() != nil {
		return
	}
	fin := time.Now().UTC()
	for _, t := range tasks {
		t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		if err := o.store.UpdateTask(ctx, o.org(ctx), t); err != nil {
			o.log.Warn("update task", "err", err)
		}
		o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": t}, Text: fmt.Sprintf("Tarea cancelada (%s): %s", why, t.Title)})
	}
	o.emitMetrics(ctx)
}

// ---- reserve / pause / resume ----

// reserveFor returns the amount to hold for a call: the upper bound of the
// estimate when known, else coarse constants.
func (o *Orchestrator) reserveFor(ctx context.Context, requestID string, kind domain.UsageKind, taskID string) float64 {
	e, ok := o.budget.Estimate(ctx, requestID)
	switch kind {
	case domain.UsageRunTask:
		if ok {
			for _, t := range e.Tasks {
				if t.TaskID == taskID {
					return t.MaxUSD
				}
			}
		}
		_, hi := fallbackTaskRange("", 2)
		return hi
	case domain.UsageSynthesize:
		if ok {
			return e.Synthesis.MaxUSD
		}
		return recalcCost("", fallbackInMax, fallbackOutMax)
	default: // consult
		return recalcCost("", consultReserveIn, consultReserveOut)
	}
}

// reserveOrPause reserves budget for one runtime call. When a request or agent
// cap blocks it, the call PAUSES (never silently): a budget.exceeded event
// explains what happened and the call resumes as soon as the cap is raised.
// The organization budget is not resumable: it fails with errBudget.
func (o *Orchestrator) reserveOrPause(ctx context.Context, rs *run, agentID, taskID string, kind domain.UsageKind) (*Reservation, error) {
	org := o.org(ctx)
	est := o.reserveFor(ctx, rs.req.ID, kind, taskID)
	var timer *time.Timer
	var paused *Exceeded
	leave := func() {
		if paused != nil {
			o.leavePause(ctx, rs, paused)
			paused = nil
		}
	}
	for {
		wake := o.budget.WakeChan()
		res, ex, err := o.budget.Reserve(ctx, rs.req.ID, agentID, est)
		if err != nil {
			leave()
			return nil, err
		}
		if ex == nil {
			leave()
			return res, nil
		}
		ex.TaskID = taskID
		if !ex.Resumable() {
			leave()
			o.announceExceeded(ctx, rs, ex, true)
			return nil, errBudget
		}
		entering := paused == nil
		if paused != nil && (paused.Scope != ex.Scope || paused.ScopeID != ex.ScopeID) {
			leave()
			entering = true
		}
		if entering {
			// Only the call that opens the pause episode explains it; the others
			// that hit the same cap join it silently (no duplicate messages).
			if o.budget.enterPause(org, ex) {
				o.announceExceeded(ctx, rs, ex, true)
			}
		} else {
			// Woken by a cap change but still blocked: refresh the numbers.
			o.announceExceeded(ctx, rs, ex, false)
		}
		paused = ex
		if timer == nil {
			timer = time.NewTimer(o.cfg.PauseTimeout)
			defer timer.Stop()
		}
		resume := YieldSlot(ctx) // a task paused by a cap does not hold a scheduler slot
		select {
		case <-wake:
			resume()
		case <-ctx.Done():
			resume()
			leave()
			return nil, ctx.Err()
		case <-timer.C:
			resume()
			leave()
			return nil, fmt.Errorf("la pausa por tope de presupuesto no se resolvió a tiempo (%s %s)", ex.Scope, ex.ScopeID)
		}
	}
}

// announceExceeded makes a budget stop visible: audit, event, activity item,
// request/agent state and a plain message to the user. `first` marks the start
// of a pause episode (the heavier side effects run once per episode).
func (o *Orchestrator) announceExceeded(ctx context.Context, rs *run, ex *Exceeded, first bool) {
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "budget.exceeded", Entity: string(ex.Scope), EntityID: ex.ScopeID,
		Details: map[string]any{"scope": ex.Scope, "cap_usd": ex.CapUSD, "spent_usd": ex.SpentUSD, "reserved_usd": ex.ReservedUSD,
			"needed_usd": ex.NeededUSD, "request_id": ex.RequestID, "agent_id": ex.AgentID, "task_id": ex.TaskID}})
	msg := o.exceededMessage(ex)
	act := Action{Type: domain.EvBudgetExceeded, AgentID: ex.AgentID, SkipAudit: true, Entity: string(ex.Scope), EntityID: ex.ScopeID,
		Payload: map[string]any{"scope": ex.Scope, "scope_id": ex.ScopeID, "request_id": ex.RequestID, "agent_id": ex.AgentID,
			"task_id": ex.TaskID, "cap_usd": ex.CapUSD, "spent_usd": ex.SpentUSD, "reserved_usd": ex.ReservedUSD,
			"needed_usd": ex.NeededUSD, "resumable": ex.Resumable(), "message": msg}}
	if first {
		act.Text = msg
	}
	o.rec.Emit(ctx, act)
	if !first {
		return
	}
	switch ex.Scope {
	case domain.ScopeRequest:
		o.setRequestStatus(ctx, rs, domain.RequestPaused)
		o.setState(ctx, assistantID, domain.StateBlocked, "Pausado: tope de presupuesto de la solicitud", nil, 30)
	case domain.ScopeAgent:
		var tid *string
		if ex.TaskID != "" {
			tid = &ex.TaskID
		}
		o.setState(ctx, ex.AgentID, domain.StateBlocked, "Pausado: tope de presupuesto del agente", tid, 0)
	}
	o.sendMessage(ctx, rs, "system", "user", "chat", msg, nil)
	o.emitMetrics(ctx)
}

func (o *Orchestrator) exceededMessage(ex *Exceeded) string {
	switch ex.Scope {
	case domain.ScopeRequest:
		return fmt.Sprintf("Solicitud en pausa: gastó $%.2f de su tope de $%.2f y la siguiente llamada necesita llegar a $%.2f. Sube el tope para continuar.",
			ex.SpentUSD, ex.CapUSD, ex.NeededUSD)
	case domain.ScopeAgent:
		return fmt.Sprintf("Agente %s en pausa: gastó $%.2f de su tope mensual de $%.2f y la siguiente llamada necesita llegar a $%.2f. Sube el tope para continuar.",
			ex.AgentID, ex.SpentUSD, ex.CapUSD, ex.NeededUSD)
	}
	return fmt.Sprintf("Presupuesto de la organización agotado: gastado $%.2f de $%.2f. No se inician más llamadas.", ex.SpentUSD, ex.CapUSD)
}

// leavePause ends one waiter's pause; the last one resumes the request/agent.
func (o *Orchestrator) leavePause(ctx context.Context, rs *run, ex *Exceeded) {
	if !o.budget.leavePause(o.org(ctx), ex) || ctx.Err() != nil {
		return
	}
	o.rec.Emit(ctx, Action{Type: domain.EvBudgetResumed, AgentID: ex.AgentID, Entity: string(ex.Scope), EntityID: ex.ScopeID,
		Payload: map[string]any{"scope": ex.Scope, "scope_id": ex.ScopeID, "request_id": ex.RequestID, "agent_id": ex.AgentID},
		Text:    "Presupuesto ampliado: se reanuda el trabajo"})
	if ex.Scope == domain.ScopeRequest {
		rs.mu.Lock()
		paused := rs.req.Status == domain.RequestPaused
		rs.mu.Unlock()
		if paused {
			o.setRequestStatus(ctx, rs, domain.RequestRunning)
			o.setState(ctx, assistantID, domain.StateWaiting, "Coordinando al equipo", nil, 30)
		}
	}
	o.emitMetrics(ctx)
}

// recordUsage reconciles a finished call: ledger + totals + audit, then it
// releases the reservation. Calls without any usage only release.
func (o *Orchestrator) recordUsage(ctx context.Context, rs *run, taskID, agentID string, kind domain.UsageKind, u Usage, tools []ToolRequest, res *Reservation) {
	defer res.Release()
	if u.CostUSD <= 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Tool+"."+t.Action)
	}
	entry := domain.UsageEntry{TaskID: taskID, AgentID: agentID, Kind: kind, Model: u.Model,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, Tools: names}
	cost, err := o.budget.Record(ctx, rs.req.ID, entry, u.CostUSD)
	if err != nil {
		o.log.Warn("record usage", "err", err)
	}
	u.CostUSD = cost
	o.rec.Audit(ctx, domain.AuditLog{Actor: agentID, Action: "runtime.usage", Entity: "request", EntityID: rs.req.ID, Details: u})
	o.emitMetrics(ctx)
}

// isCostGate reports the request states that only cost control produces; their
// transitions are published as request.status_changed.
func isCostGate(st domain.RequestStatus) bool {
	return st == domain.RequestPaused || st == domain.RequestAwaitingConfirmation
}
