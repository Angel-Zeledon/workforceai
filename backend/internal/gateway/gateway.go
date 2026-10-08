// Package gateway is the Tool Gateway: the only place where a connection's
// credential is used. It re-validates controls, grants and limits at execution
// time, runs the provider call through the vault-backed HTTP client and hands
// back ONLY sanitized, delimited results. Secrets never leave this process
// boundary: not to the frontend, the runtime, prompts, logs or events.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/sanitize"
)

// MinHoldSeconds is the mandatory cancellation window of an email send
// (owner decision 3). Configuration can only raise it.
const MinHoldSeconds = 60

// MaxHoldSeconds caps the configurable window.
const MaxHoldSeconds = 300

// Deny codes owned by the gateway.
const (
	CodeApprovalMismatch = "approval_mismatch"
	CodeNoSecrets        = connections.CodeSecretDetected
)

// Gateway wires connections, controls and the sanitizer.
type Gateway struct {
	Conns    *connections.Service
	Controls *controls.Service
	Suspects *sanitize.Suspects
	Log      *slog.Logger
	Now      func() time.Time
	Emit     connections.EmitFunc
	Audit    connections.AuditFunc
	// HoldSeconds is the send window; values below MinHoldSeconds are raised.
	HoldSeconds int
	// OrgIDs lists the organizations visited by background sweeps (provider
	// revocation retries). Optional.
	OrgIDs func(ctx context.Context) []string

	mu        sync.Mutex
	seen      map[string]map[string]bool // org/task -> addresses seen in external content
	planStore *planStore
	approvals map[string]application.GatewayPending
	// outbox (see outbox.go)
	entries     map[string]*outboxEntry
	byKey       map[string]string // approval key -> entry id
	approvedOut map[string]application.GatewayOutcome
}

// New builds a Gateway.
func New(cs *connections.Service, ctl *controls.Service, sus *sanitize.Suspects, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	return &Gateway{Conns: cs, Controls: ctl, Suspects: sus, Log: log, Now: time.Now, HoldSeconds: MinHoldSeconds,
		Emit: func(context.Context, string, string, map[string]any) {}, Audit: func(context.Context, string, string, string, string, string, map[string]any) {},
		seen: map[string]map[string]bool{}, approvals: map[string]application.GatewayPending{},
		entries: map[string]*outboxEntry{}, byKey: map[string]string{}, approvedOut: map[string]application.GatewayOutcome{}}
}

var _ application.ToolGateway = (*Gateway)(nil)

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// HoldWindow returns the effective send window.
func (g *Gateway) HoldWindow() time.Duration {
	s := g.HoldSeconds
	if s < MinHoldSeconds {
		s = MinHoldSeconds
	}
	if s > MaxHoldSeconds {
		s = MaxHoldSeconds
	}
	return time.Duration(s) * time.Second
}

type target struct {
	provider connections.Manifest
	cap      string
	spec     connections.CapabilitySpec
}

// lookup maps tool+action to a provider capability.
func (g *Gateway) lookup(tool, action string) (target, bool) {
	tool, action = strings.ToLower(tool), strings.ToLower(action)
	ids := make([]string, 0, len(g.Conns.Manifests()))
	for id := range g.Conns.Manifests() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := g.Conns.Manifests()[id]
		if c, ok := m.CapabilityForTool(tool, action); ok {
			return target{provider: m, cap: c, spec: m.Capabilities[c]}, true
		}
	}
	return target{}, false
}

// IsSideEffect reports whether a tool action can change the outside world.
// Connection tools use their manifest; for any other tool unknown actions
// count as side effects (fail closed) unless they are well-known reads.
func (g *Gateway) IsSideEffect(tool, action string) bool {
	if t, ok := g.lookup(tool, action); ok {
		return t.spec.SideEffects
	}
	switch strings.ToLower(action) {
	case "list", "read", "get", "search", "query", "view", "summarize", "analyze", "calculate", "evaluate", "describe", "lookup", "find", "count", "check":
		return false
	}
	return true
}

// Route implements application.ToolGateway.
func (g *Gateway) Route(ctx context.Context, org, agentID, tool, action string) application.GatewayRoute {
	t, ok := g.lookup(tool, action)
	if !ok {
		return application.RouteNone
	}
	cs, err := g.Conns.Store.ListConnections(ctx, org, t.provider.ID, "")
	if err == nil && len(cs) == 0 {
		return application.RouteNone // no real/simulated connection: legacy simulation
	}
	if t.spec.SideEffects {
		return application.RouteWrite
	}
	return application.RouteRead
}

// ApprovalContext returns the structured context registered for an approval.
func (g *Gateway) ApprovalContext(id string) (application.GatewayPending, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.approvals[id]
	return p, ok
}

// ArgsHash binds an approval to the exact call.
func ArgsHash(tool, action string, args map[string]any) string {
	clean := map[string]any{}
	for k, v := range args {
		if k == "connection_alias" {
			continue
		}
		clean[k] = v
	}
	b, _ := json.Marshal(map[string]any{"tool": strings.ToLower(tool), "action": strings.ToLower(action), "args": clean})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var autonomyRank = map[string]int{"suggest": 0, "approve_each": 1, "rules": 2, "autonomous": 3}

func effectiveAutonomy(agent, override string) string {
	a, ok := autonomyRank[agent]
	if !ok {
		return "suggest" // unknown: most restrictive
	}
	if o, ok := autonomyRank[override]; ok && o < a {
		return override
	}
	return agent
}

// candidate is a (connection, grant) pair able to serve a capability.
type candidate struct {
	conn  connections.Connection
	grant connections.Grant
}

// resolve picks the connection through the agent's grants (D1: connected is
// not granted). The task names a tool, never a connection; an optional
// connection_alias is validated against the grants.
func (g *Gateway) resolve(ctx context.Context, c application.GatewayCall, t target) (candidate, string) {
	gs, err := g.Conns.Store.ListGrantsByAgent(ctx, c.Org, c.AgentID)
	if err != nil {
		return candidate{}, controls.CodeUnavailable
	}
	alias, _ := c.Args["connection_alias"].(string)
	alias = strings.ToLower(strings.TrimSpace(alias))
	now := g.now()
	var cands []candidate
	for _, gr := range gs {
		if gr.Status != connections.GrantActive || now.Before(gr.ValidFrom) || (gr.ValidUntil != nil && now.After(*gr.ValidUntil)) {
			continue
		}
		has := false
		for _, cp := range gr.Capabilities {
			has = has || cp == t.cap
		}
		if !has {
			continue
		}
		cn, err := g.Conns.Store.GetConnection(ctx, c.Org, gr.ConnectionID)
		if err != nil || cn.Provider != t.provider.ID {
			continue
		}
		if alias != "" && gr.Alias != alias {
			continue
		}
		cands = append(cands, candidate{cn, gr})
	}
	if len(cands) == 0 {
		return candidate{}, connections.CodeScopeNotGranted
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].grant.IsDefault && !cands[j].grant.IsDefault })
	return cands[0], ""
}

// validate re-checks everything that can change between request time and
// execution time (TOCTOU): controls, connection status, scopes, risk, limits.
func (g *Gateway) validate(ctx context.Context, c application.GatewayCall, t target, cd candidate, skipLimits bool) string {
	side := t.spec.SideEffects
	if v := g.Controls.CheckTool(ctx, c.Org, c.AgentID, c.Tool, c.Action, side); !v.Allowed {
		return v.Code
	}
	if c.ReadOnlyRequest && side {
		return connections.CodeReadOnlyMode
	}
	switch cd.conn.Status {
	case connections.StatusActive:
	case connections.StatusNeedsReauth:
		return connections.CodeReauthRequired
	default:
		return connections.CodeConnectionUnavailable
	}
	if cd.conn.ReadOnly && side {
		return connections.CodeReadOnlyMode
	}
	has := false
	for _, cp := range cd.conn.GrantedCapabilities {
		has = has || cp == t.cap
	}
	if !has {
		return connections.CodeScopeNotGranted
	}
	if connections.RiskRank(t.spec.Risk) > connections.RiskRank(cd.grant.MaxRisk) {
		return connections.CodeRiskExceedsGrant
	}
	if skipLimits {
		return "" // a deferred send already counted when it was scheduled
	}
	code, err := g.Conns.LimitCheck(ctx, c.Org, cd.conn, cd.grant, side)
	if err != nil {
		return controls.CodeUnavailable
	}
	return code
}

var addrRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

func recipientsOf(args map[string]any) []string {
	var out []string
	for _, k := range []string{"to", "cc", "attendees"} { // calendar invites reach people too
		switch v := args[k].(type) {
		case string:
			out = append(out, addrRe.FindAllString(v, -1)...)
		case []any:
			for _, x := range v {
				out = append(out, addrRe.FindAllString(fmt.Sprint(x), -1)...)
			}
		case []string:
			for _, x := range v {
				out = append(out, addrRe.FindAllString(x, -1)...)
			}
		}
	}
	seen := map[string]bool{}
	var uniq []string
	for _, r := range out {
		r = strings.ToLower(r)
		if !seen[r] {
			seen[r] = true
			uniq = append(uniq, r)
		}
	}
	return uniq
}

func domainOf(addr string) string {
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

func (g *Gateway) seenKey(org, task string) string { return org + "/" + task }

func (g *Gateway) remember(org, task string, texts []string) {
	if task == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := g.seenKey(org, task)
	set := g.seen[k]
	if set == nil {
		if len(g.seen) > 2000 { // bound memory
			g.seen = map[string]map[string]bool{}
		}
		set = map[string]bool{}
		g.seen[k] = set
	}
	for _, t := range texts {
		for _, a := range addrRe.FindAllString(t, 50) {
			set[strings.ToLower(a)] = true
		}
	}
}

func (g *Gateway) wasSeen(org, task, addr string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seen[g.seenKey(org, task)][strings.ToLower(addr)]
}

// Execute implements application.ToolGateway.
func (g *Gateway) Execute(ctx context.Context, c application.GatewayCall) application.GatewayOutcome {
	t, ok := g.lookup(c.Tool, c.Action)
	if !ok {
		return application.GatewayOutcome{Decision: "denied", DenyReason: connections.CodeInvalidArgs}
	}
	approved := c.ApprovedArgsHash != ""
	if !approved { // the approved re-execution is the same request: count it once
		g.Controls.ObserveToolRequest(ctx, c.Org, c.AgentID)
		if t.spec.SideEffects {
			g.Controls.ObserveRecipients(ctx, c.Org, recipientsOf(c.Args))
		}
	}
	if approved {
		// Idempotent: the outbox and the waiting task may both execute the same
		// approval; the first one wins and the second gets the same outcome.
		if prev, ok := g.approvedOutcome(c.ApprovalID); ok && c.ApprovalID != "" {
			return prev
		}
		if c.ApprovedArgsHash != ArgsHash(c.Tool, c.Action, c.Args) {
			return g.deny(ctx, c, t, candidate{}, CodeApprovalMismatch)
		}
		// Editing an item invalidates the approval of its old content.
		if e := g.entryForKey(c.ApprovalID); e != nil && e.hash() != c.ApprovedArgsHash {
			return g.deny(ctx, c, t, candidate{}, CodeApprovalMismatch)
		}
	}

	// Controls first: kill switch / pause / read-only apply even before we
	// know which connection would serve the call.
	if v := g.Controls.CheckTool(ctx, c.Org, c.AgentID, c.Tool, c.Action, t.spec.SideEffects); !v.Allowed {
		return g.deny(ctx, c, t, candidate{}, v.Code)
	}
	cd, code := g.resolve(ctx, c, t)
	if code != "" {
		return g.deny(ctx, c, t, candidate{}, code)
	}
	if code := g.validate(ctx, c, t, cd, false); code != "" {
		return g.deny(ctx, c, t, cd, code)
	}

	side := t.spec.SideEffects
	var rcpt []string
	if side {
		// Outgoing content must not carry credentials.
		if g.hasSecret(c.Args) {
			g.Emit(ctx, c.Org, "security.alert", map[string]any{"kind": "secret_detected", "agent_id": c.AgentID, "tool": c.Tool, "action": c.Action})
			return g.deny(ctx, c, t, cd, connections.CodeSecretDetected)
		}
		rcpt = recipientsOf(c.Args)
	}

	if !approved {
		if reasons := g.approvalReasons(c, t, cd, rcpt); len(reasons) > 0 {
			return g.pending(ctx, c, t, cd, rcpt, reasons)
		}
	}

	var out application.GatewayOutcome
	if side && t.spec.AlwaysApproval {
		// Only reachable with an approval for this exact call: defer the send.
		out = g.schedule(ctx, c, t, cd, rcpt)
	} else {
		out = g.run(ctx, c, t, cd, rcpt, false)
	}
	if approved && c.ApprovalID != "" && out.Decision == "allowed" && (out.Status == "scheduled" || out.Status == "succeeded") {
		g.rememberApproved(c.ApprovalID, out)
	}
	return out
}

// approvalReasons lists why this call needs a human (empty = may run).
func (g *Gateway) approvalReasons(c application.GatewayCall, t target, cd candidate, rcpt []string) []string {
	if !t.spec.SideEffects {
		return nil
	}
	var r []string
	if t.spec.AlwaysApproval {
		r = append(r, "always_approval") // invariant: external, irreversible effects
	}
	if c.Tainted {
		r = append(r, "tainted_task")
	}
	if c.InjectionSuspected {
		r = append(r, "injection_suspected")
	}
	for _, id := range c.ReadConnections {
		if id != cd.conn.ID {
			r = append(r, "cross_connection")
			break
		}
	}
	switch effectiveAutonomy(c.Autonomy, cd.grant.AutonomyOverride) {
	case "suggest", "approve_each":
		r = append(r, "autonomy")
	}
	return r
}

// buildPending computes the approval card of a write (pure: no side effects).
func (g *Gateway) buildPending(c application.GatewayCall, t target, cd candidate, rcpt, reasons []string) *application.GatewayPending {
	p := &application.GatewayPending{ArgsHash: ArgsHash(c.Tool, c.Action, c.Args), Risk: t.spec.Risk, Reversibility: t.spec.Reversibility,
		Account: cd.conn.AccountLabel, Recipients: rcpt, Tainted: c.Tainted, Flags: reasons}
	if p.Reversibility == "" {
		p.Reversibility = "none"
	}
	if t.spec.AlwaysApproval {
		p.HoldSeconds = int(g.HoldWindow() / time.Second)
	}
	allow := cd.grant.Constraints.AllowedRecipientDomains
	for _, r := range rcpt {
		if len(allow) > 0 && !contains(allow, domainOf(r)) {
			p.Flags = appendOnce(p.Flags, "recipient_outside_allowlist")
		}
		if c.Tainted && g.wasSeen(c.Org, c.TaskID, r) && !contains(allow, domainOf(r)) {
			p.ExternalOrigin = true
			p.Flags = appendOnce(p.Flags, "recipient_from_external_content")
		}
	}
	p.Title = fmt.Sprintf("%s.%s from %s", c.Tool, c.Action, cd.conn.AccountLabel)
	subj := headline(c.Args)
	p.Details = fmt.Sprintf("account=%s recipients=%s subject=%q reversibility=%s hold_seconds=%d flags=%s",
		cd.conn.AccountLabel, strings.Join(rcpt, ","), sanitize.SanitizeInline(subj, 120, sanitize.Options{Suspects: g.Suspects}),
		p.Reversibility, p.HoldSeconds, strings.Join(p.Flags, ","))
	return p
}

func (g *Gateway) pending(ctx context.Context, c application.GatewayCall, t target, cd candidate, rcpt, reasons []string) application.GatewayOutcome {
	p := g.buildPending(c, t, cd, rcpt, reasons)
	g.newEntry(c, p, cd) // the outbox item a human can read, edit, approve or reject
	g.usage(ctx, c, t, cd, connections.Usage{Decision: "needs_approval", Status: "held", ResourceRef: fmt.Sprintf("recipients=%d", len(rcpt)), Tainted: c.Tainted})
	return application.GatewayOutcome{Decision: "needs_approval", Pending: p, ConnectionID: cd.conn.ID, Tainted: c.Tainted}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func appendOnce(l []string, s string) []string {
	if contains(l, s) {
		return l
	}
	return append(l, s)
}

// hasSecret scans every string of the outgoing args.
func (g *Gateway) hasSecret(args map[string]any) bool {
	var found bool
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if sanitize.RedactSecrets(x, g.Suspects) != x {
				found = true
			}
		case []any:
			for _, i := range x {
				walk(i)
			}
		case map[string]any:
			for _, i := range x {
				walk(i)
			}
		}
	}
	walk(args)
	return found
}

func (g *Gateway) deny(ctx context.Context, c application.GatewayCall, t target, cd candidate, code string) application.GatewayOutcome {
	if cd.conn.ID != "" {
		g.usage(ctx, c, t, cd, connections.Usage{Decision: "denied", DenyReason: code, Status: "skipped", Tainted: c.Tainted})
		if code == connections.CodeRateLimit {
			g.Emit(ctx, c.Org, "connection.rate_limited", map[string]any{"connection_id": cd.conn.ID, "agent_id": c.AgentID, "limit": "rate"})
		}
	} else {
		g.Audit(ctx, c.Org, c.AgentID, "connection.use", "connection", "", map[string]any{"tool": c.Tool, "action": c.Action,
			"capability": t.cap, "decision": "denied", "reason": code, "task_id": c.TaskID})
	}
	return application.GatewayOutcome{Decision: "denied", DenyReason: code, ConnectionID: cd.conn.ID}
}

// usage writes the usage projection row plus the audit entry (metadata only).
func (g *Gateway) usage(ctx context.Context, c application.GatewayCall, t target, cd candidate, u connections.Usage) {
	u.OrgID, u.ConnectionID, u.GrantID, u.AgentID, u.TaskID = c.Org, cd.conn.ID, cd.grant.ID, c.AgentID, c.TaskID
	u.ApprovalID, u.OnBehalfOf, u.Tool, u.Action, u.Capability = c.ApprovalID, c.OnBehalfOf, c.Tool, c.Action, t.cap
	g.Conns.RecordUsage(ctx, u)
	g.Audit(ctx, c.Org, c.AgentID, "connection.use", "connection", cd.conn.ID, map[string]any{
		"tool": c.Tool, "action": c.Action, "capability": t.cap, "decision": u.Decision, "reason": u.DenyReason,
		"status": u.Status, "grant_id": cd.grant.ID, "task_id": c.TaskID, "on_behalf_of": c.OnBehalfOf,
		"approval_id": c.ApprovalID, "resource_ref": u.ResourceRef, "items": u.ItemsCount, "tainted": u.Tainted})
}

func minPositive(vals ...int) int {
	m := 0
	for _, v := range vals {
		if v > 0 && (m == 0 || v < m) {
			m = v
		}
	}
	return m
}

// run executes a read, or a reversible write (draft), right now.
func (g *Gateway) run(ctx context.Context, c application.GatewayCall, t target, cd candidate, rcpt []string, fromHold bool) application.GatewayOutcome {
	ex, err := g.Conns.Executor(cd.conn)
	if err != nil {
		return g.deny(ctx, c, t, cd, connections.CodeConnectionUnavailable)
	}
	maxItems := minPositive(cd.grant.Limits.MaxItemsPerCall, cd.conn.Limits.MaxItemsPerCall, 50)
	if v, ok := c.Args["max_results"].(float64); ok && int(v) > 0 {
		maxItems = minPositive(maxItems, int(v))
	}
	args := map[string]any{}
	for k, v := range c.Args {
		if k != "connection_alias" {
			args[k] = v
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	res, err := ex.Execute(cctx, connections.ExecCall{Tool: c.Tool, Action: c.Action, Args: args, MaxItems: maxItems})
	lat := int(time.Since(start).Milliseconds())
	if err != nil {
		return g.fail(ctx, c, t, cd, err, res.Status, lat)
	}

	out := application.GatewayOutcome{Decision: "allowed", Status: "succeeded", Summary: res.Summary, ConnectionID: cd.conn.ID}
	if !t.spec.SideEffects {
		blocks, suspected, seenTexts := g.sanitizeItems(c, t, cd, res.Items)
		g.remember(c.Org, c.TaskID, seenTexts)
		out.Blocks, out.Tainted, out.InjectionSuspected = blocks, true, suspected
		if suspected {
			g.Emit(ctx, c.Org, "security.alert", map[string]any{"kind": "prompt_injection_suspected", "agent_id": c.AgentID, "tool": c.Tool, "connection_id": cd.conn.ID})
			g.Audit(ctx, c.Org, c.AgentID, "security.injection_suspected", "connection", cd.conn.ID, map[string]any{"tool": c.Tool, "task_id": c.TaskID})
		}
		if len(res.Items) > 0 {
			out.Summary = fmt.Sprintf("%d item(s) returned as delimited untrusted data", len(res.Items))
		}
	} else if res.Data != nil {
		// Structured, trusted ids only (draft id); never message text.
		if id, _ := res.Data["draft_id"].(string); id != "" {
			out.Summary = "draft created (not sent): " + id
		}
		for _, k := range []string{"event_id", "issue_url", "comment_url", "message_ts"} {
			if id, _ := res.Data[k].(string); id != "" {
				out.Summary = res.Summary + ": " + id
			}
		}
	}
	status := "succeeded"
	if fromHold {
		status = "sent" // the schedule row already counted against the limits
	}
	g.usage(ctx, c, t, cd, connections.Usage{Decision: "allowed", Status: status, ResourceRef: sanitize.SanitizeInline(res.ResourceRef, 120, sanitize.Options{Suspects: g.Suspects}),
		ItemsCount: len(res.Items), BytesIn: res.BytesIn, BytesOut: res.BytesOut, LatencyMS: lat, ProviderState: res.Status, Tainted: out.Tainted})
	return out
}

func (g *Gateway) fail(ctx context.Context, c application.GatewayCall, t target, cd candidate, err error, status, lat int) application.GatewayOutcome {
	pe := connections.Classify(err)
	code := pe.Code
	switch code {
	case connections.CodeReauthRequired:
		g.Conns.MarkNeedsReauth(ctx, c.Org, cd.conn.ID)
	case "insufficient_scope":
		code = connections.CodeScopeNotGranted
	case "rate_limited":
		code = connections.CodeRateLimit
	case "not_found", connections.CodeInvalidArgs, connections.CodeOutOfScope, connections.CodeConnectionUnavailable:
	default:
		code = connections.CodeProviderError
	}
	g.usage(ctx, c, t, cd, connections.Usage{Decision: "allowed", Status: "failed", ErrorCode: code, LatencyMS: lat, ProviderState: pe.Status, Tainted: c.Tainted})
	return application.GatewayOutcome{Decision: "allowed", Status: "failed", DenyReason: code, Summary: "provider call failed: " + code, ConnectionID: cd.conn.ID}
}

func fieldLimit(name string) int {
	switch name {
	case "body":
		return 4000
	case "snippet":
		return 400
	}
	return 300
}

// sanitizeItems normalizes, redacts, neutralizes and wraps every item as
// delimited untrusted data. It also returns the raw text seen (for recipient
// provenance); that text is never returned to callers.
func (g *Gateway) sanitizeItems(c application.GatewayCall, t target, cd candidate, items []connections.Item) (blocks []string, suspected bool, seen []string) {
	profile := cd.grant.RedactionProfile
	if profile == "" {
		profile = sanitize.DefaultProfile // strict (owner decision 6)
	}
	source := "tool:" + strings.ToLower(c.Tool) + "." + strings.ToLower(c.Action)
	for _, it := range items {
		var b strings.Builder
		from := ""
		for _, tx := range it.Texts {
			seen = append(seen, tx.Value)
			o := sanitize.Sanitize(tx.Value, sanitize.Options{Profile: profile, MaxChars: fieldLimit(tx.Name), Suspects: g.Suspects})
			text := o.Text
			if tx.Name != "body" {
				text = strings.ReplaceAll(text, "\n", " ")
			}
			if tx.Name == "from" {
				from = text
			}
			if o.InjectionSuspect {
				suspected = true
			}
			fmt.Fprintf(&b, "%s: %s\n", tx.Name, text)
		}
		for _, k := range sortedKeys(it.Meta) {
			fmt.Fprintf(&b, "%s: %s\n", k, metaString(it.Meta[k], g.Suspects))
		}
		blocks = append(blocks, sanitize.Wrap(sanitize.SanitizeInline(it.ID, 60, sanitize.Options{Suspects: g.Suspects}), source, from, strings.TrimSpace(b.String())))
	}
	return blocks, suspected, seen
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func metaString(v any, s *sanitize.Suspects) string {
	switch x := v.(type) {
	case string:
		return sanitize.SanitizeInline(x, 120, sanitize.Options{Suspects: s})
	case []string:
		return sanitize.SanitizeInline(strings.Join(x, ","), 120, sanitize.Options{Suspects: s})
	}
	return sanitize.SanitizeInline(fmt.Sprint(v), 120, sanitize.Options{Suspects: s})
}

// ---- deferred sends (mandatory cancellation window) ----

// schedule stores an approved send and returns immediately; the message goes
// out only when the window ends (ProcessDue) and everything re-validates.
func (g *Gateway) schedule(ctx context.Context, c application.GatewayCall, t target, cd candidate, rcpt []string) application.GatewayOutcome {
	until := g.now().Add(g.HoldWindow())
	payload := map[string]any{}
	for k, v := range c.Args {
		if k != "connection_alias" {
			payload[k] = v
		}
	}
	holdID := uuid.NewString()
	if e := g.entryForKey(c.ApprovalID); e != nil {
		holdID = e.id // one id for the item's whole life
	}
	h := connections.Hold{ID: holdID, OrgID: c.Org, ConnectionID: cd.conn.ID, GrantID: cd.grant.ID, AgentID: c.AgentID,
		TaskID: c.TaskID, ApprovalID: c.ApprovalID, Tool: c.Tool, Action: c.Action, Capability: t.cap, Payload: payload,
		Summary: g.subjectSummary(payload), Recipients: rcpt, Status: connections.HoldHeld, HoldUntil: until,
		CreatedAt: g.now(), Tainted: c.Tainted}
	if h.Recipients == nil {
		h.Recipients = []string{}
	}
	if err := g.Conns.Store.PutHold(ctx, h); err != nil {
		return g.deny(ctx, c, t, cd, controls.CodeUnavailable)
	}
	g.mu.Lock()
	delete(g.entries, h.ID) // the hold now represents the item
	g.mu.Unlock()
	g.usage(ctx, c, t, cd, connections.Usage{Decision: "allowed", Status: "scheduled", ResourceRef: fmt.Sprintf("recipients=%d hold_id=%s", len(rcpt), h.ID), Tainted: c.Tainted})
	g.Emit(ctx, c.Org, "tool_call.hold_changed", map[string]any{"hold_id": h.ID, "status": h.Status, "hold_until": until, "agent_id": c.AgentID,
		"recipients": h.Recipients, "tool_call": g.holdItem(ctx, h)})
	return application.GatewayOutcome{Decision: "allowed", Status: "scheduled", ConnectionID: cd.conn.ID, HoldID: h.ID, HoldUntil: &until,
		Summary: fmt.Sprintf("send scheduled; it can be cancelled until %s", until.Format(time.RFC3339))}
}

// Holds lists holds of an organization (payload never included).
func (g *Gateway) Holds(ctx context.Context, org, status string) ([]connections.Hold, error) {
	hs, err := g.Conns.Store.ListHolds(ctx, org, status)
	if hs == nil {
		hs = []connections.Hold{}
	}
	return hs, err
}

// CancelHold cancels a held send before it goes out.
func (g *Gateway) CancelHold(ctx context.Context, org, id, actor string) (connections.Hold, error) {
	h, err := g.Conns.Store.GetHold(ctx, org, id)
	if err != nil {
		return connections.Hold{}, err
	}
	if h.Status != connections.HoldHeld {
		return h, fmt.Errorf("%w: hold is %s", connections.ErrConflict, h.Status)
	}
	return g.finishHold(ctx, h, connections.HoldCancelled, "cancelled_by_user", actor), nil
}

func (g *Gateway) finishHold(ctx context.Context, h connections.Hold, status, reason, actor string) connections.Hold {
	h.Status, h.Reason, h.DecidedBy = status, reason, actor
	h.Payload = nil // message content is dropped once the hold is decided
	_ = g.Conns.Store.UpdateHold(context.WithoutCancel(ctx), h)
	g.Audit(ctx, h.OrgID, firstNonEmpty(actor, "system"), "tool_call.hold_"+status, "connection", h.ConnectionID, map[string]any{"hold_id": h.ID, "reason": reason, "agent_id": h.AgentID})
	g.Emit(ctx, h.OrgID, "tool_call.hold_changed", map[string]any{"hold_id": h.ID, "status": status, "reason": reason, "tool_call": g.holdItem(ctx, h)})
	return h
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CancelHoldsFor cancels every held send of a connection (revoke/suspend).
func (g *Gateway) CancelHoldsFor(ctx context.Context, org, connID, reason string) int {
	hs, _ := g.Conns.Store.ListHolds(ctx, org, connections.HoldHeld)
	n := 0
	for _, h := range hs {
		if connID == "" || h.ConnectionID == connID {
			g.finishHold(ctx, h, connections.HoldCancelled, reason, "system")
			n++
		}
	}
	return n
}

// ProcessDue sends the holds whose window ended, after re-validating controls,
// connection, grant and limits. Anything that no longer holds is cancelled.
func (g *Gateway) ProcessDue(ctx context.Context) int {
	hs, err := g.Conns.Store.ListDueHolds(ctx, g.now())
	if err != nil {
		return 0
	}
	sent := 0
	for _, h := range hs {
		if g.sendHold(ctx, h) {
			sent++
		}
	}
	return sent
}

func (g *Gateway) sendHold(ctx context.Context, h connections.Hold) bool {
	t, ok := g.lookup(h.Tool, h.Action)
	if !ok {
		g.finishHold(ctx, h, connections.HoldFailed, connections.CodeInvalidArgs, "system")
		return false
	}
	call := application.GatewayCall{Org: h.OrgID, AgentID: h.AgentID, TaskID: h.TaskID, Tool: h.Tool, Action: h.Action, Args: h.Payload,
		ApprovalID: h.ApprovalID, Tainted: h.Tainted}
	conn, err := g.Conns.Store.GetConnection(ctx, h.OrgID, h.ConnectionID)
	if err != nil {
		g.finishHold(ctx, h, connections.HoldCancelled, connections.CodeConnectionUnavailable, "system")
		return false
	}
	gr, err := g.Conns.Store.GetGrant(ctx, h.OrgID, h.ConnectionID, h.AgentID)
	now := g.now()
	if err != nil || gr.Status != connections.GrantActive || (gr.ValidUntil != nil && now.After(*gr.ValidUntil)) || !contains(gr.Capabilities, t.cap) {
		g.finishHold(ctx, h, connections.HoldCancelled, connections.CodeScopeNotGranted, "system")
		return false
	}
	cd := candidate{conn: conn, grant: gr}
	if code := g.validate(ctx, call, t, cd, true); code != "" {
		if code == controls.CodeUnavailable {
			return false // transient: retry on the next tick
		}
		if controlCodes[code] {
			g.requeue(ctx, h, code)
		} else {
			g.finishHold(ctx, h, connections.HoldCancelled, code, "system")
		}
		g.usage(ctx, call, t, cd, connections.Usage{Decision: "denied", DenyReason: code, Status: "skipped"})
		return false
	}
	out := g.run(ctx, call, t, cd, h.Recipients, true)
	if out.Status != "succeeded" {
		g.finishHold(ctx, h, connections.HoldFailed, out.DenyReason, "system")
		return false
	}
	g.finishHold(ctx, h, connections.HoldSent, "", "system")
	return true
}

// Run processes due holds and pending provider revocations until ctx ends.
func (g *Gateway) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.ProcessDue(ctx)
			if n%60 == 0 && g.OrgIDs != nil {
				for _, org := range g.OrgIDs(ctx) {
					g.Conns.SweepRevocations(ctx, org)
				}
			}
		}
	}
}

// ---- control hooks ----

// OnKillSwitch cancels every held send; lockdown also suspends all connections.
func (g *Gateway) OnKillSwitch(ctx context.Context, org, level, actor string) {
	g.RequeueHolds(ctx, org, "kill_switch") // nothing runs by inertia: a human approves again
	if level == controls.LevelLockdown {
		g.Conns.SuspendAll(ctx, org, actor, "kill_switch")
	}
}

// OnRelease optionally resumes the connections a lockdown suspended.
func (g *Gateway) OnRelease(ctx context.Context, org, actor string, resume bool) {
	if resume {
		g.Conns.ResumeAll(ctx, org, actor)
	}
}
