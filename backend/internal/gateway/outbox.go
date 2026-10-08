package gateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/sanitize"
)

// The outbox is the human's view of everything an agent wants to write
// through a connection: drafts and sends waiting for approval, sends waiting
// out their cancellation window, and what already happened. An item keeps ONE
// id for its whole life (pending entry -> hold), so the same id cancels,
// approves or rejects it.

// Outbox item statuses.
const (
	OutboxPendingApproval = "pending_approval"
	OutboxHeld            = "held"
	OutboxSent            = "sent"
	OutboxCancelled       = "cancelled"
	OutboxRejected        = "rejected"
	OutboxBlocked         = "blocked"
)

// ErrVersionMismatch: the item was edited after the approver loaded it.
var ErrVersionMismatch = errors.New("gateway: the item changed; reload it before approving")

// DeniedError carries the stable deny code of a refused approval.
type DeniedError struct{ Code string }

func (e *DeniedError) Error() string { return "denied: " + e.Code }

// OutboxItem is the API view (secret-free; the body is what the agent wrote,
// scanned for credentials before it was accepted).
type OutboxItem struct {
	ID             string     `json:"id"`
	ApprovalID     string     `json:"approval_id,omitempty"`
	AgentID        string     `json:"agent_id"`
	ConnectionID   string     `json:"connection_id"`
	AccountLabel   string     `json:"account_label"`
	Tool           string     `json:"tool"`
	Action         string     `json:"action"`
	To             []string   `json:"to"`
	Cc             []string   `json:"cc"`
	Subject        string     `json:"subject"`
	Body           string     `json:"body"`
	Status         string     `json:"status"`
	BlockReason    string     `json:"block_reason"`
	OriginExternal bool       `json:"origin_external"`
	NewRecipient   bool       `json:"new_recipient"`
	Tainted        bool       `json:"tainted"`
	Reversibility  string     `json:"reversibility"`
	HoldSeconds    int        `json:"hold_seconds"`
	HoldUntil      *time.Time `json:"hold_until"`
	Flags          []string   `json:"flags"`
	Version        int        `json:"version"`
	ArgsHash       string     `json:"args_hash"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type outboxEntry struct {
	id         string
	call       application.GatewayCall
	pending    application.GatewayPending
	connID     string
	account    string
	approvalID string
	round      int
	version    int
	status     string
	reason     string
	createdAt  time.Time
	updatedAt  time.Time
}

func (e *outboxEntry) key() string {
	if e.approvalID != "" {
		return e.approvalID
	}
	return fmt.Sprintf("outbox:%s#%d", e.id, e.round)
}

func (e *outboxEntry) hash() string { return ArgsHash(e.call.Tool, e.call.Action, e.call.Args) }

func cloneArgs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if k != "connection_alias" {
			out[k] = v
		}
	}
	return out
}

func strList(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = addrRe.FindAllString(x, -1)
	case []any:
		for _, i := range x {
			out = append(out, addrRe.FindAllString(fmt.Sprint(i), -1)...)
		}
	case []string:
		for _, i := range x {
			out = append(out, addrRe.FindAllString(i, -1)...)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

const maxEntries = 500

// newEntry registers a pending write as an outbox entry.
func (g *Gateway) newEntry(c application.GatewayCall, p *application.GatewayPending, cd candidate) *outboxEntry {
	now := g.now()
	e := &outboxEntry{id: "ob_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], call: c, pending: *p, connID: cd.conn.ID,
		account: cd.conn.AccountLabel, version: 1, status: OutboxPendingApproval, createdAt: now, updatedAt: now}
	e.call.Args = cloneArgs(c.Args)
	e.call.ApprovalID, e.call.ApprovedArgsHash = "", ""
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.entries) >= maxEntries { // bound memory: drop the oldest decided entries
		for id, x := range g.entries {
			if x.status != OutboxPendingApproval {
				delete(g.entries, id)
			}
		}
	}
	g.entries[e.id] = e
	p.OutboxID = e.id
	return e
}

func (g *Gateway) entryForKey(key string) *outboxEntry {
	if key == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if id, ok := g.byKey[key]; ok {
		return g.entries[id]
	}
	return nil
}

func (g *Gateway) approvedOutcome(key string) (application.GatewayOutcome, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.approvedOut[key]
	return o, ok
}

func (g *Gateway) rememberApproved(key string, o application.GatewayOutcome) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.approvedOut) > 2000 {
		g.approvedOut = map[string]application.GatewayOutcome{}
	}
	g.approvedOut[key] = o
}

// RegisterApproval implements application.ToolGateway: it remembers the card
// context and binds the pending outbox entry to its approval.
func (g *Gateway) RegisterApproval(id string, p application.GatewayPending) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.approvals[id] = p
	if e, ok := g.entries[p.OutboxID]; ok {
		e.approvalID = id
		g.byKey[id] = e.id
	}
}

// ApprovalResolved implements application.ToolGateway: a rejected (or timed
// out) approval closes its outbox entry.
func (g *Gateway) ApprovalResolved(approvalID string, approved bool) {
	if approved {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if id, ok := g.byKey[approvalID]; ok {
		if e := g.entries[id]; e != nil && e.status == OutboxPendingApproval {
			e.status, e.updatedAt = OutboxRejected, g.now()
		}
	}
}

func (g *Gateway) entryItem(e *outboxEntry) OutboxItem {
	a := e.call.Args
	return OutboxItem{ID: e.id, ApprovalID: e.approvalID, AgentID: e.call.AgentID, ConnectionID: e.connID, AccountLabel: e.account,
		Tool: e.call.Tool, Action: e.call.Action, To: strList(a["to"]), Cc: strList(a["cc"]), Subject: str(a["subject"]), Body: str(a["body"]),
		Status: e.status, BlockReason: e.reason, OriginExternal: e.pending.ExternalOrigin, NewRecipient: contains(e.pending.Flags, "recipient_outside_allowlist"),
		Tainted: e.pending.Tainted, Reversibility: e.pending.Reversibility, HoldSeconds: e.pending.HoldSeconds, Flags: nonNil(e.pending.Flags),
		Version: e.version, ArgsHash: e.hash(), CreatedAt: e.createdAt, UpdatedAt: e.updatedAt}
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// holdItem builds the item of a hold (sent/cancelled holds have no payload any more).
func (g *Gateway) holdItem(ctx context.Context, h connections.Hold) OutboxItem {
	it := OutboxItem{ID: h.ID, ApprovalID: h.ApprovalID, AgentID: h.AgentID, ConnectionID: h.ConnectionID, Tool: h.Tool, Action: h.Action,
		To: nonNil(h.Recipients), Cc: strList(h.Payload["cc"]), Subject: h.Summary, Body: str(h.Payload["body"]), BlockReason: h.Reason, Tainted: h.Tainted,
		Reversibility: "none", HoldSeconds: int(g.HoldWindow() / time.Second), Flags: []string{}, Version: 1, CreatedAt: h.CreatedAt, UpdatedAt: h.CreatedAt}
	switch h.Status {
	case connections.HoldHeld:
		it.Status, it.HoldUntil = OutboxHeld, &h.HoldUntil
	case connections.HoldSent:
		it.Status, it.HoldUntil = OutboxSent, &h.HoldUntil
	case connections.HoldCancelled:
		it.Status = OutboxCancelled
	default:
		it.Status = OutboxBlocked
	}
	if c, err := g.Conns.Store.GetConnection(ctx, h.OrgID, h.ConnectionID); err == nil {
		it.AccountLabel = c.AccountLabel
	}
	return it
}

// HoldItem is the outbox view of a hold.
func (g *Gateway) HoldItem(ctx context.Context, h connections.Hold) OutboxItem {
	return g.holdItem(ctx, h)
}

// Outbox lists items newest first; status "" = all.
func (g *Gateway) Outbox(ctx context.Context, org, status string) ([]OutboxItem, error) {
	hs, err := g.Conns.Store.ListHolds(ctx, org, "")
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	pendingIDs := map[string]bool{}
	out := []OutboxItem{}
	for _, e := range g.entries {
		if e.call.Org == org {
			pendingIDs[e.id] = true
			out = append(out, g.entryItem(e))
		}
	}
	g.mu.Unlock()
	for _, h := range hs {
		if !pendingIDs[h.ID] { // a requeued item shows as pending, not as its old hold
			out = append(out, g.holdItem(ctx, h))
		}
	}
	if status != "" {
		kept := out[:0]
		for _, it := range out {
			if it.Status == status {
				kept = append(kept, it)
			}
		}
		out = kept
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (g *Gateway) pendingEntry(org, id string) (*outboxEntry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.entries[id]
	if !ok || e.call.Org != org {
		return nil, connections.ErrNotFound
	}
	return e, nil
}

// OutboxPatch is what a human may edit before approving. Editing invalidates
// any earlier approval of the old content: the version and args_hash change.
type OutboxPatch struct {
	To      *[]string
	Cc      *[]string
	Subject *string
	Body    *string
}

// OutboxEdit edits a pending item (what a human may change before approving).
// Editing invalidates any earlier approval of the old content: the version and
// args_hash change.
func (g *Gateway) OutboxEdit(ctx context.Context, org, id string, p OutboxPatch) (OutboxItem, error) {
	e, err := g.pendingEntry(org, id)
	if err != nil {
		return OutboxItem{}, err
	}
	g.mu.Lock()
	if e.status != OutboxPendingApproval {
		g.mu.Unlock()
		return OutboxItem{}, fmt.Errorf("%w: item is %s", connections.ErrConflict, e.status)
	}
	next := cloneArgs(e.call.Args)
	if p.To != nil {
		next["to"] = *p.To
	}
	if p.Cc != nil {
		next["cc"] = *p.Cc
	}
	if p.Subject != nil {
		next["subject"] = *p.Subject
	}
	if p.Body != nil {
		next["body"] = *p.Body
	}
	call := e.call
	g.mu.Unlock()

	if g.hasSecret(next) {
		g.Emit(ctx, org, "security.alert", map[string]any{"kind": "secret_detected", "tool": call.Tool, "action": call.Action})
		return OutboxItem{}, fmt.Errorf("%w: %s: the text looks like it contains a credential", connections.ErrInvalid, connections.CodeSecretDetected)
	}
	rcpt := recipientsOf(next)
	if len(rcpt) == 0 {
		return OutboxItem{}, fmt.Errorf("%w: at least one valid recipient is required", connections.ErrInvalid)
	}
	// Recompute the card: recipients changed, so allowlist/provenance flags too.
	call.Args = next
	var np *application.GatewayPending
	if t, ok := g.lookup(call.Tool, call.Action); ok {
		if cd, code := g.resolve(ctx, call, t); code == "" {
			np = g.buildPending(call, t, cd, rcpt, g.approvalReasons(call, t, cd, rcpt))
			np.OutboxID = e.id
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if e.status != OutboxPendingApproval {
		return OutboxItem{}, fmt.Errorf("%w: item is %s", connections.ErrConflict, e.status)
	}
	e.call.Args = next
	e.version++
	e.updatedAt = g.now()
	if np != nil {
		e.pending = *np
	}
	return g.entryItem(e), nil
}

// OutboxApprove approves item id at the given version: the send is scheduled
// (it still waits out its 60 s window). decide, when set, resolves the pending
// approval record so the waiting task continues.
func (g *Gateway) OutboxApprove(ctx context.Context, org, id string, version int, actor string, decide func(approvalID string) error) (OutboxItem, error) {
	e, err := g.pendingEntry(org, id)
	if err != nil {
		return OutboxItem{}, err
	}
	g.mu.Lock()
	if e.status != OutboxPendingApproval {
		g.mu.Unlock()
		return OutboxItem{}, fmt.Errorf("%w: item is %s", connections.ErrConflict, e.status)
	}
	if version != e.version {
		g.mu.Unlock()
		return OutboxItem{}, ErrVersionMismatch
	}
	call := e.call
	call.Args = cloneArgs(e.call.Args)
	call.ApprovalID, call.ApprovedArgsHash = e.key(), e.hash()
	g.byKey[call.ApprovalID] = e.id
	approvalID := e.approvalID
	g.mu.Unlock()

	out := g.Execute(ctx, call)
	if out.Decision == "denied" {
		return OutboxItem{}, &DeniedError{Code: out.DenyReason}
	}
	g.Audit(ctx, org, actor, "outbox.approved", "connection", e.connID, map[string]any{"item_id": id, "version": version, "hold_id": out.HoldID})
	if approvalID != "" && decide != nil {
		_ = decide(approvalID) // already-resolved is fine: the first decision wins
	}
	if out.HoldID != "" {
		if h, err := g.Conns.Store.GetHold(ctx, org, out.HoldID); err == nil {
			return g.holdItem(ctx, h), nil
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.entryItem(e), nil
}

// OutboxReject rejects a pending item.
func (g *Gateway) OutboxReject(ctx context.Context, org, id, actor string, decide func(approvalID string) error) (OutboxItem, error) {
	e, err := g.pendingEntry(org, id)
	if err != nil {
		return OutboxItem{}, err
	}
	g.mu.Lock()
	if e.status != OutboxPendingApproval {
		g.mu.Unlock()
		return OutboxItem{}, fmt.Errorf("%w: item is %s", connections.ErrConflict, e.status)
	}
	e.status, e.reason, e.updatedAt = OutboxRejected, "rejected_by_user", g.now()
	approvalID := e.approvalID
	it := g.entryItem(e)
	g.mu.Unlock()
	g.Audit(ctx, org, actor, "outbox.rejected", "connection", e.connID, map[string]any{"item_id": id})
	g.Emit(ctx, org, "tool_call.hold_changed", map[string]any{"hold_id": id, "status": OutboxRejected, "tool_call": it})
	if approvalID != "" && decide != nil {
		_ = decide(approvalID)
	}
	return it, nil
}

// RequeueHolds returns every held send of the organization to the approval
// queue (kill switch, read-only mode, pause): nothing executes by inertia, a
// human approves it again. The message content moves to the outbox entry.
func (g *Gateway) RequeueHolds(ctx context.Context, org, reason string) int {
	hs, _ := g.Conns.Store.ListHolds(ctx, org, connections.HoldHeld)
	n := 0
	for _, h := range hs {
		g.requeue(ctx, h, reason)
		n++
	}
	return n
}

func (g *Gateway) requeue(ctx context.Context, h connections.Hold, reason string) {
	t, ok := g.lookup(h.Tool, h.Action)
	if !ok || len(h.Payload) == 0 {
		g.finishHold(ctx, h, connections.HoldCancelled, reason, "system")
		return
	}
	now := g.now()
	args := cloneArgs(h.Payload)
	label := ""
	if c, err := g.Conns.Store.GetConnection(ctx, h.OrgID, h.ConnectionID); err == nil {
		label = c.AccountLabel
	}
	e := &outboxEntry{id: h.ID, call: application.GatewayCall{Org: h.OrgID, AgentID: h.AgentID, TaskID: h.TaskID, Tool: h.Tool, Action: h.Action, Args: args,
		Autonomy: "approve_each", Tainted: h.Tainted},
		pending: application.GatewayPending{Risk: t.spec.Risk, Reversibility: "none", Account: label, Recipients: h.Recipients, Tainted: h.Tainted,
			HoldSeconds: int(g.HoldWindow() / time.Second), Flags: []string{"requeued:" + reason}, OutboxID: h.ID},
		connID: h.ConnectionID, account: label, round: 1, version: 1, status: OutboxPendingApproval, reason: reason, createdAt: h.CreatedAt, updatedAt: now}
	g.mu.Lock()
	if old := g.entries[h.ID]; old != nil {
		e.round = old.round + 1
	}
	g.entries[h.ID] = e
	g.mu.Unlock()
	g.finishHold(ctx, h, connections.HoldCancelled, reason, "system")
	g.mu.Lock()
	it := g.entryItem(e)
	g.mu.Unlock()
	g.Emit(ctx, h.OrgID, "tool_call.hold_changed", map[string]any{"hold_id": h.ID, "status": OutboxPendingApproval, "reason": reason, "tool_call": it})
}

// controlCodes are denials that put a held send back in the queue instead of
// destroying it: the human decides again once the control is lifted.
var controlCodes = map[string]bool{
	controls.CodeKillSwitch: true, controls.CodeAgentPaused: true, controls.CodeReadOnly: true, controls.CodeToolDisabled: true,
}

// subjectSummary is the content-free label stored on a hold.
func (g *Gateway) subjectSummary(args map[string]any) string {
	return sanitize.SanitizeInline(headline(args), 120, sanitize.Options{Suspects: g.Suspects})
}

// headline is the short label of an outgoing item: the email subject, or the
// title/summary of an event, issue or chat message.
func headline(args map[string]any) string {
	for _, k := range []string{"subject", "title", "summary"} {
		if v := str(args[k]); v != "" {
			return v
		}
	}
	return ""
}
