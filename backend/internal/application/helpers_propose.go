package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Proposing temporary helpers (design section 6, "Proponer" and "Aceptar"; docs/architecture/chat-routing.md).
//
// Between the plan review and the cost confirmation a request may be OFFERED to the person as a split: the runtime
// says (POST /v1/split) whether it is worth dividing into 4 or more independent parts. The offer is a helper group in
// state "proposed", stored (it survives a restart) with its estimate. The request waits for the person in the same
// way it waits for a cost confirmation (a durable gate, status awaiting_confirmation, no scheduler slot held); the
// person accepts (AcceptHelpers: the parts and the joining task are created, the plan's own tasks are left out) or
// declines (DeclineHelpers: the request follows its normal plan). Nothing is spent on the helpers until they accept.
// Only the split call itself is spent (ledger, caps), and when it fails or the runtime cannot split there is simply
// no proposal.

const (
	// minHelperParts is the smallest split worth proposing (design: 4 or more independent parts).
	minHelperParts = 4
	// maxHelperParts is the contract maximum of /v1/split.
	maxHelperParts    = 50
	helperSplitWait   = 45 * time.Second
	helperPollEvery   = 300 * time.Millisecond
	helperNotifyAfter = 150 * time.Millisecond
)

type helpersFreeKey struct{}

// WithHelpersFree marks a chat message as "helpers without a position": the office coordinator takes the request and
// the split is attempted whatever its plan says. Only valid in the office conversation (postChat checks it).
func WithHelpersFree(ctx context.Context) context.Context {
	return context.WithValue(ctx, helpersFreeKey{}, true)
}

func helpersFreeFrom(ctx context.Context) bool {
	v, _ := ctx.Value(helpersFreeKey{}).(bool)
	return v
}

// HelpersEnabled reports whether temporary helpers are on (HELPERS_ENABLED) and have a store.
func (o *Orchestrator) HelpersEnabled() bool { return o.cfg.HelpersEnabled && o.helpers.store != nil }

// helperCoordinator picks who coordinates a request of no department: the agent of role "assistant" the person can
// ask for work, else the first agent of their office. Errors: 404 with the flag off, 400 outside the office chat or
// without anyone to coordinate.
func (o *Orchestrator) helperCoordinator(_ context.Context, t *chatTurn) (string, error) {
	if !o.HelpersEnabled() {
		return "", fmt.Errorf("%w: helpers_disabled", domain.ErrNotFound)
	}
	if t.pub != domain.ChatOffice {
		return "", fmt.Errorf("%w: helpers without a position are asked in the office conversation", domain.ErrInvalid)
	}
	for _, a := range t.agents {
		if a.Role == "assistant" && a.ID != assistantID {
			return a.ID, nil
		}
	}
	for _, a := range t.agents {
		if a.ID != assistantID {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("%w: there is nobody in your office to coordinate the helpers", domain.ErrInvalid)
}

// attachedPaths are the drive paths of the delimited [attachments] block of a request text (chat_files.go).
func attachedPaths(text string) []string {
	i := strings.Index(text, "[attachments]")
	j := strings.Index(text, "[/attachments]")
	if i < 0 || j < i {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(text[i:j], "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(ln), "- "); ok && strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return out
}

var helperNotices = map[string]map[string]string{
	"es": {"no_split": "No pude repartirlo entre ayudantes (%s). Sigo con el pedido como siempre."},
	"en": {"no_split": "I could not split it among helpers (%s). I carry on with the request as usual."},
	"pt": {"no_split": "Não consegui dividir entre ajudantes (%s). Sigo com o pedido como sempre."},
}

func helperNotice(loc, key string) string {
	if m, ok := helperNotices[loc]; ok {
		return m[key]
	}
	return helperNotices["es"][key]
}

// ---- the proposal ----

// helperProposalStep runs between the plan review and the cost confirmation. proposed=false: nothing was offered,
// carry on. Otherwise it waits for the person and returns the tasks to run: accepted=true means the plan's own
// tasks are left out and the parts and the joining task run instead (their cost was accepted: no second prompt).
// ok=false: the request must stop (shutdown).
func (o *Orchestrator) helperProposalStep(ctx context.Context, rs *run, tasks []domain.Task) (out []domain.Task, accepted, ok bool) {
	if _, proposed := o.maybeProposeHelpers(ctx, rs, tasks); !proposed {
		return tasks, false, true
	}
	return o.helperAwait(ctx, rs, tasks, time.Now().UTC())
}

// maybeProposeHelpers asks the runtime whether the request is worth splitting and, when it is, stores the proposal.
func (o *Orchestrator) maybeProposeHelpers(ctx context.Context, rs *run, tasks []domain.Task) (domain.HelperGroup, bool) {
	if !o.HelpersEnabled() || rs.ext.rehearsal {
		return domain.HelperGroup{}, false
	}
	sr, can := o.rt.(SplitRuntime)
	free := rs.chat != nil && rs.chat.helpersFree
	if rs.preset != nil && !free { // a workflow template or a project plan is not split
		return domain.HelperGroup{}, false
	}
	var own []domain.Task
	for _, t := range tasks {
		if t.Helper == nil {
			own = append(own, t)
		}
	}
	owner := ""
	switch {
	case free && rs.chat != nil:
		owner = rs.chat.speaker
	case len(own) == 1:
		owner = own[0].AgentID
	}
	agent, known := rs.agents[owner]
	if owner == "" || owner == assistantID || !known {
		return domain.HelperGroup{}, false
	}
	org := o.org(ctx)
	hs := o.helpers.store
	if g, err := hs.GetHelperGroupByRequest(ctx, org, rs.req.ID); err == nil { // already proposed (a restart): not twice
		return g, g.Status == domain.HelperProposed
	}
	why := ""
	defer func() {
		if free && why != "" {
			o.chatSay(ctx, rs, owner, fmt.Sprintf(helperNotice(rs.chat.loc, "no_split"), why))
		}
	}()
	if !can {
		why = "no_runtime"
		return domain.HelperGroup{}, false
	}
	parallel, maxParts, _ := o.helperLimits(ctx, org)
	maxParts = min(maxParts, maxHelperParts)
	if maxParts < minHelperParts {
		why = "plan"
		return domain.HelperGroup{}, false
	}
	var items []SplitItem
	byRef := map[string]domain.FileRef{}
	for i, p := range attachedPaths(rs.req.Text) {
		ref := fmt.Sprintf("f%d", i+1)
		items = append(items, SplitItem{Name: path.Base(p), Ref: ref})
		byRef[ref] = domain.FileRef{Path: p, Name: path.Base(p), Action: "attach"}
	}
	in := SplitRequest{Text: truncate(rs.req.Text, 6000), Items: items, MaxParts: maxParts, Locale: rs.style.Locale}
	res, ex := o.chatReserve(ctx, owner, UsageSplit, in) // the split call is real spend: under the org and agent caps
	if ex != nil {
		why = "budget"
		return domain.HelperGroup{}, false
	}
	cctx, cancel := context.WithTimeout(ctx, helperSplitWait)
	resp, err := sr.Split(cctx, in)
	cancel()
	if err != nil {
		res.Release()
		if ctx.Err() == nil {
			o.log.Warn("helpers: split failed, normal plan", "request", rs.req.ID, "err", err)
			o.rec.Audit(ctx, domain.AuditLog{Actor: owner, Action: "helpers.split_failed", Entity: "request", EntityID: rs.req.ID, RequestID: rs.req.ID,
				Details: map[string]any{"error": truncate(err.Error(), 200)}})
		}
		why = "runtime"
		return domain.HelperGroup{}, false
	}
	var usage Usage
	if resp.Usage != nil {
		usage = *resp.Usage
	}
	o.recordUsage(ctx, rs, "", owner, domain.UsagePlan, usage, nil, res) // ledger, request total, audit; releases the hold
	if !resp.WorthIt || len(resp.Parts) < minHelperParts || len(resp.Parts) > maxParts {
		why = resp.Reason
		if why == "" {
			why = "not independent"
		}
		return domain.HelperGroup{}, false
	}
	parts := make([]domain.HelperPart, 0, len(resp.Parts))
	for i, p := range resp.Parts {
		hp := domain.HelperPart{Index: i + 1, Label: strings.TrimSpace(p.Label), Text: strings.TrimSpace(p.Instructions)}
		for _, ref := range p.ItemRefs {
			if f, ok := byRef[ref]; ok {
				hp.Files = append(hp.Files, f)
			}
		}
		if hp.Label == "" {
			hp.Label = fmt.Sprintf("Parte %d", i+1)
		}
		parts = append(parts, hp)
	}
	est := o.helperEstimate(ctx, rs, owner, parts)
	mode := domain.HelperModePosition
	if free {
		mode = domain.HelperModeFree
	}
	g := domain.HelperGroup{ID: newID(), RequestID: rs.req.ID, OwnerAgentID: agent.ID, Mode: mode, Count: len(parts), MaxCount: maxParts,
		Parallel: min(parallel, len(parts)), Parts: parts, Estimate: est, CapUSD: est.MaxUSD, Status: domain.HelperProposed, CreatedAt: time.Now().UTC()}
	if err := hs.CreateHelperGroup(ctx, org, g); err != nil {
		return domain.HelperGroup{}, false
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: owner, Action: "helpers.proposed", Entity: "request", EntityID: rs.req.ID, RequestID: rs.req.ID,
		Details: map[string]any{"group_id": g.ID, "count": g.Count, "mode": mode, "estimate_min_usd": est.MinUSD, "estimate_max_usd": est.MaxUSD, "source": resp.Source}})
	return g, true
}

// helperEstimate is the cost range of the whole split (the parts and the joining task): /v1/estimate or the fallback.
func (o *Orchestrator) helperEstimate(ctx context.Context, rs *run, owner string, parts []domain.HelperPart) domain.HelperEstimate {
	syn := make([]domain.Task, 0, len(parts)+1)
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		id := fmt.Sprintf("part%d", p.Index)
		ids = append(ids, id)
		syn = append(syn, domain.Task{ID: id, Title: p.Label, Description: helperPartDescription(p, len(parts)), AgentID: owner})
	}
	syn = append(syn, domain.Task{ID: "union", Title: "Juntar los resultados", Description: helperUnionDescription(rs.req.Text, len(parts)), AgentID: owner, DependsOn: ids})
	e := o.buildEstimate(ctx, rs.req.ID, rs.req.Text, syn)
	return domain.HelperEstimate{MinUSD: roundUSD(e.Total.MinUSD), MaxUSD: roundUSD(e.Total.MaxUSD)}
}

// helperProposalHolds reports whether the request waits on its helper proposal (a group that is proposed, or that was
// just accepted and whose request has not been released yet).
func (o *Orchestrator) helperProposalHolds(ctx context.Context, requestID string) bool {
	if o.helpers.store == nil {
		return false
	}
	g, err := o.helpers.store.GetHelperGroupByRequest(ctx, o.org(ctx), requestID)
	return err == nil && (g.Status == domain.HelperProposed || g.Status == domain.HelperRunning)
}

func roundUSD(v float64) float64 { return math.Round(v*1e5) / 1e5 }

// ---- waiting for the person ----

func helperWaitKey(org, req string) string { return org + "|" + req }

// helperWaiter registers where the decision of a proposal is delivered (before anything can decide it).
func (o *Orchestrator) helperWaiter(org, req string) (chan bool, func()) {
	ch := make(chan bool, 4)
	k := helperWaitKey(org, req)
	o.helpers.wmu.Lock()
	if o.helpers.wait == nil {
		o.helpers.wait = map[string]chan bool{}
	}
	o.helpers.wait[k] = ch
	o.helpers.wmu.Unlock()
	return ch, func() {
		o.helpers.wmu.Lock()
		if o.helpers.wait[k] == ch {
			delete(o.helpers.wait, k)
		}
		o.helpers.wmu.Unlock()
	}
}

func (o *Orchestrator) helperSignal(org, req string, accepted bool) {
	o.helpers.wmu.Lock()
	ch := o.helpers.wait[helperWaitKey(org, req)]
	o.helpers.wmu.Unlock()
	if ch != nil {
		select {
		case ch <- accepted:
		default:
		}
	}
}

// helperAwait holds the request until the person decides on the stored proposal (also after a restart: the stored
// group says what was decided meanwhile) and returns the tasks to run.
func (o *Orchestrator) helperAwait(ctx context.Context, rs *run, tasks []domain.Task, since time.Time) (out []domain.Task, accepted, ok bool) {
	org := o.org(ctx)
	hs := o.helpers.store
	ch, drop := o.helperWaiter(org, rs.req.ID)
	defer drop()
	g, err := hs.GetHelperGroupByRequest(ctx, org, rs.req.ID)
	if err != nil {
		return tasks, false, true // no proposal on record: the normal plan
	}
	if g.Status == domain.HelperProposed {
		o.setGate(ctx, rs, &RunGate{Kind: GateHelperProposal, StartedAt: since})
		o.setRequestStatus(ctx, rs, domain.RequestAwaitingConfirmation)
		o.setState(ctx, assistantID, domain.StateWaiting, "Esperando tu decisión sobre los ayudantes", nil, 20)
		o.emitMetrics(ctx)
		o.helperEmit(ctx, domain.EvHelpersProposed, g)
	}
	timer := time.NewTimer(max(time.Until(since.Add(o.cfg.ApprovalTimeout)), 0))
	defer timer.Stop()
	tick := time.NewTicker(helperPollEvery)
	defer tick.Stop()
	for {
		cur, err := hs.GetHelperGroup(ctx, org, g.ID)
		if err != nil {
			return tasks, false, ctx.Err() == nil
		}
		if cur.Status != domain.HelperProposed {
			o.setGate(ctx, rs, nil)
			if cur.Status == domain.HelperDeclined {
				o.setRequestStatus(ctx, rs, domain.RequestPlanning)
				return tasks, false, true
			}
			return o.helperAccepted(ctx, rs, cur), true, true
		}
		select {
		case <-ch:
		case <-tick.C:
		case <-timer.C: // nobody answered in time: the request carries on as the normal plan (nothing was spent on helpers)
			if _, err := o.declineGroup(ctx, cur, "system"); err != nil && !errors.Is(err, domain.ErrConflict) {
				o.log.Warn("helpers: decline on timeout", "request", rs.req.ID, "err", err)
			}
		case <-ctx.Done():
			return tasks, false, false
		}
	}
}

// helperAccepted leaves out the plan's own tasks (the parts and the joining task replace them) and returns what runs.
func (o *Orchestrator) helperAccepted(ctx context.Context, rs *run, g domain.HelperGroup) []domain.Task {
	org := o.org(ctx)
	all, err := o.store.ListTasksByRequest(ctx, org, rs.req.ID)
	if err != nil {
		o.log.Warn("helpers: list tasks", "request", rs.req.ID, "err", err)
		return nil
	}
	rs.mu.Lock()
	for _, t := range all {
		if t.Helper == nil && !slices.Contains(rs.removed, t.ID) {
			rs.removed = append(rs.removed, t.ID)
		}
	}
	rs.mu.Unlock()
	o.saveRunMeta(ctx, rs)
	for _, t := range all {
		rs.touch(t.AgentID)
	}
	applyRemoved(rs, all)
	return all
}

// estimateAccepted records the estimate of the accepted tasks without asking again: the person already accepted
// the cost (and a hard cap) when accepting the helpers.
func (o *Orchestrator) estimateAccepted(ctx context.Context, rs *run, tasks []domain.Task) {
	var live []domain.Task
	for _, t := range tasks {
		if t.Helper != nil {
			live = append(live, t)
		}
	}
	est := o.buildEstimate(ctx, rs.req.ID, rs.req.Text, live)
	est.RequiresConfirmation, est.ConfirmReason = false, ""
	o.budget.setEstimate(o.org(ctx), est)
	o.rec.Emit(ctx, Action{Type: domain.EvCostEstimated, AgentID: assistantID, Entity: "request", EntityID: rs.req.ID,
		Payload: map[string]any{"request_id": rs.req.ID, "estimate": est},
		Text:    fmt.Sprintf("Costo estimado: entre $%.2f y $%.2f", est.Total.MinUSD, est.Total.MaxUSD)})
}

// ---- the person decides ----

// helperCapRoom is how much a group could still spend under the caps that already apply (the organization budget
// and the cap of the request); +Inf when none applies.
func (o *Orchestrator) helperCapRoom(ctx context.Context, requestID string) float64 {
	room := math.Inf(1)
	org := o.org(ctx)
	if o.cfg.BudgetUSD > 0 {
		if used, err := o.store.OrgCost(ctx, org); err == nil {
			room = math.Min(room, o.cfg.BudgetUSD-used-o.chatSpend(ctx))
		}
	}
	if c := o.requestCap(ctx, requestID); c > 0 {
		spent := 0.0
		if r, err := o.store.GetRequest(ctx, org, requestID); err == nil {
			spent = r.CostUSD
		}
		room = math.Min(room, c-spent)
	}
	return math.Max(room, 0)
}

// AcceptHelpers launches the proposed helpers: count helpers (merging neighbouring parts when fewer than the parts)
// under a hard cap of capUSD. Idempotent: a group already launched is returned as it is. Errors: ErrNotFound (flag
// off, no group), ErrConflict (declined), ErrInvalid (count or cap out of range), the plan limit error.
func (o *Orchestrator) AcceptHelpers(ctx context.Context, requestID string, count int, capUSD float64, actor string) (domain.HelperGroup, error) {
	if !o.HelpersEnabled() {
		return domain.HelperGroup{}, fmt.Errorf("%w: helpers_disabled", domain.ErrNotFound)
	}
	org := o.org(ctx)
	g, err := o.helpers.store.GetHelperGroupByRequest(ctx, org, requestID)
	if err != nil {
		return domain.HelperGroup{}, err
	}
	switch g.Status {
	case domain.HelperProposed:
	case domain.HelperDeclined:
		return domain.HelperGroup{}, fmt.Errorf("%w: the helpers were declined", domain.ErrConflict)
	default:
		return o.helperView(ctx, g.ID) // already launched: the same group, nothing is created twice
	}
	switch {
	case count < 2:
		return domain.HelperGroup{}, fmt.Errorf("%w: at least 2 helpers", domain.ErrInvalid)
	case count > g.MaxCount || count > len(g.Parts):
		return domain.HelperGroup{}, fmt.Errorf("%w: at most %d helpers", domain.ErrInvalid, min(g.MaxCount, len(g.Parts)))
	case capUSD < g.Estimate.MinUSD || capUSD <= 0:
		return domain.HelperGroup{}, fmt.Errorf("%w: the cap ($%.2f) is below the minimum estimate ($%.2f)", domain.ErrInvalid, capUSD, g.Estimate.MinUSD)
	}
	if room := o.helperCapRoom(ctx, requestID); capUSD > room+1e-9 {
		return domain.HelperGroup{}, fmt.Errorf("%w: the cap ($%.2f) is above what the budget still allows ($%.2f)", domain.ErrInvalid, capUSD, room)
	}
	out, err := o.LaunchHelpers(ctx, g, count, capUSD, actor)
	if err != nil {
		return domain.HelperGroup{}, err
	}
	o.helperSignal(org, requestID, true)
	o.helperEmit(ctx, domain.EvHelpersUpdated, out)
	return out, nil
}

// DeclineHelpers leaves the request to its normal plan. Idempotent on a declined group; ErrConflict when the
// helpers were already launched.
func (o *Orchestrator) DeclineHelpers(ctx context.Context, requestID, actor string) (domain.HelperGroup, error) {
	if !o.HelpersEnabled() {
		return domain.HelperGroup{}, fmt.Errorf("%w: helpers_disabled", domain.ErrNotFound)
	}
	g, err := o.helpers.store.GetHelperGroupByRequest(ctx, o.org(ctx), requestID)
	if err != nil {
		return domain.HelperGroup{}, err
	}
	return o.declineGroup(ctx, g, actor)
}

func (o *Orchestrator) declineGroup(ctx context.Context, g domain.HelperGroup, actor string) (domain.HelperGroup, error) {
	org := o.org(ctx)
	unlock := o.helperKeyLock("launch|" + org + "|" + g.RequestID)
	defer unlock()
	cur, err := o.helpers.store.GetHelperGroup(ctx, org, g.ID)
	if err != nil {
		return domain.HelperGroup{}, err
	}
	switch cur.Status {
	case domain.HelperDeclined:
		return cur, nil
	case domain.HelperProposed:
	default:
		return domain.HelperGroup{}, fmt.Errorf("%w: the helpers were already launched", domain.ErrConflict)
	}
	cur.Status = domain.HelperDeclined
	now := time.Now().UTC()
	cur.FinishedAt = &now
	if err := o.helpers.store.UpdateHelperGroup(ctx, org, cur); err != nil {
		return domain.HelperGroup{}, err
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "helpers.declined", Entity: "request", EntityID: cur.RequestID, RequestID: cur.RequestID,
		Details: map[string]any{"group_id": cur.ID, "count": cur.Count}})
	o.helperSignal(org, cur.RequestID, false)
	o.helperEmit(ctx, domain.EvHelpersUpdated, cur)
	return cur, nil
}

// ---- live events ----

// helperEmit publishes the group (proposed or updated) to whoever sees the request.
func (o *Orchestrator) helperEmit(ctx context.Context, typ string, g domain.HelperGroup) {
	o.rec.Emit(ctx, Action{Type: typ, AgentID: g.OwnerAgentID, Entity: "request", EntityID: g.RequestID, SkipAudit: true,
		Payload: map[string]any{"request_id": g.RequestID, "agent_id": g.OwnerAgentID, "group": g}})
}

// helperNotify publishes helpers.updated for a group, coalescing bursts (a spend per call, a part per task) into one
// event per helperNotifyAfter that carries the latest state.
func (o *Orchestrator) helperNotify(ctx context.Context, org, groupID string) {
	if o.helpers.store == nil || groupID == "" {
		return
	}
	k := org + "|" + groupID
	o.helpers.nmu.Lock()
	if o.helpers.notify == nil {
		o.helpers.notify = map[string]bool{}
	}
	if o.helpers.notify[k] {
		o.helpers.nmu.Unlock()
		return
	}
	o.helpers.notify[k] = true
	o.helpers.nmu.Unlock()
	base := WithOrg(context.WithoutCancel(ctx), org)
	time.AfterFunc(helperNotifyAfter, func() {
		o.helpers.nmu.Lock()
		delete(o.helpers.notify, k)
		o.helpers.nmu.Unlock()
		if g, err := o.helpers.store.GetHelperGroup(base, org, groupID); err == nil && g.Status != domain.HelperProposed {
			o.helperEmit(base, domain.EvHelpersUpdated, g)
		}
	})
}

// HelperGroups lists the groups of the organization that are still of interest to a live client (the hello of the
// WebSocket): proposed and in progress, and the ones that ended recently are left to GET /requests/{id}.
func (o *Orchestrator) HelperGroups(ctx context.Context) []domain.HelperGroup {
	if !o.HelpersEnabled() {
		return nil
	}
	gs, err := o.helpers.store.ListHelperGroups(ctx, o.org(ctx), domain.HelperProposed, domain.HelperRunning, domain.HelperJoining)
	if err != nil {
		return nil
	}
	return gs
}
