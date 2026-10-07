package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/policy"
)

// AuditEntry is one audit record. Args are redacted and truncated.
type AuditEntry struct {
	Time       time.Time      `json:"time"`
	Phase      string         `json:"phase"` // "decision" | "result"
	OrgID      string         `json:"org_id"`
	AgentID    string         `json:"agent_id"`
	OnBehalfOf string         `json:"on_behalf_of"`
	Chain      []string       `json:"chain,omitempty"`
	Tool       string         `json:"tool"`
	Action     string         `json:"action"`
	Args       map[string]any `json:"args,omitempty"`
	Risk       string         `json:"risk,omitempty"`
	Source     string         `json:"source,omitempty"`
	Verdict    string         `json:"verdict"` // allow|require_approval|deny
	RuleID     string         `json:"rule_id"`
	Reason     string         `json:"reason"`
	Trace      []string       `json:"trace,omitempty"`
	Status     string         `json:"status"` // see Status* constants
	Summary    string         `json:"summary,omitempty"`
	Error      string         `json:"error,omitempty"`
	ApprovedBy string         `json:"approved_by,omitempty"`
}

// AuditSink receives audit entries. If Record fails for an entry that precedes
// execution the executor fails closed and does not run the tool.
type AuditSink interface {
	Record(ctx context.Context, e AuditEntry) error
}

// Outcome statuses.
const (
	StatusExecuted        = "executed"
	StatusPendingApproval = "pending_approval"
	StatusDenied          = "denied"
	StatusFailed          = "failed"
)

// PendingApproval describes what a human must approve. Fingerprint binds the
// approval to this exact call (store it with the Approval record).
type PendingApproval struct {
	Fingerprint string         `json:"fingerprint"`
	Tool        string         `json:"tool"`
	Action      string         `json:"action"`
	Args        map[string]any `json:"args"`
	Risk        string         `json:"risk"` // low|medium|high
	Title       string         `json:"title"`
	Details     string         `json:"details"`
}

// Outcome is the result of running a ToolRequest through policy.
type Outcome struct {
	Status   string           `json:"status"`
	Decision policy.Decision  `json:"decision"`
	Result   *Result          `json:"result,omitempty"`
	Pending  *PendingApproval `json:"pending,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// Executor runs tool calls. It ALWAYS asks policy first and ALWAYS audits.
type Executor struct {
	Registry *Registry
	Policy   policy.Evaluator
	Audit    AuditSink
	Now      func() time.Time
}

// NewExecutor validates dependencies (all are mandatory).
func NewExecutor(r *Registry, p policy.Evaluator, a AuditSink) (*Executor, error) {
	if r == nil || p == nil || a == nil {
		return nil, errors.New("tools: registry, policy and audit sink are required")
	}
	return &Executor{Registry: r, Policy: p, Audit: a, Now: time.Now}, nil
}

// Execute runs a runtime tool_request on behalf of id. A returned error means an
// infrastructure failure (e.g. audit unavailable); policy denials and tool
// failures are reported in Outcome.
func (x *Executor) Execute(ctx context.Context, id policy.AgentIdentity, req ToolRequest) (Outcome, error) {
	return x.run(ctx, id, req, nil)
}

// ExecuteApproved runs a call a human approved. The approval must match the
// call's fingerprint (see Outcome.Pending.Fingerprint) and is single use.
func (x *Executor) ExecuteApproved(ctx context.Context, id policy.AgentIdentity, req ToolRequest, tok policy.ApprovalToken) (Outcome, error) {
	return x.run(ctx, id, req, &tok)
}

func (x *Executor) run(ctx context.Context, id policy.AgentIdentity, req ToolRequest, tok *policy.ApprovalToken) (Outcome, error) {
	now := x.Now
	if now == nil {
		now = time.Now
	}
	entry := func(phase string, pc policy.ToolCall, d policy.Decision, status string) AuditEntry {
		e := AuditEntry{Time: now(), Phase: phase, OrgID: id.OrgID, AgentID: id.AgentID, OnBehalfOf: id.OnBehalfOf,
			Chain: id.Chain, Tool: pc.Tool, Action: pc.Action, Args: RedactArgs(pc.Args), Risk: pc.Risk, Source: pc.Source,
			Verdict: d.Verdict(), RuleID: d.RuleID, Reason: d.Reason, Trace: d.Trace, Status: status}
		if tok != nil {
			e.ApprovedBy = tok.ApprovedBy
		}
		return e
	}

	pc, err := req.ToolCall("runtime")
	if err != nil {
		d := policy.Decision{Deny: true, RuleID: "call.invalid", Reason: err.Error()}
		// still evaluated/audited with whatever names we have
		raw := policy.ToolCall{Tool: req.Tool, Action: req.Action, Args: req.Args, Risk: req.Risk, Source: "runtime"}
		if aerr := x.Audit.Record(ctx, entry("decision", raw, d, StatusDenied)); aerr != nil {
			return Outcome{}, fmt.Errorf("tools: audit failed: %w", aerr)
		}
		return Outcome{Status: StatusDenied, Decision: d, Error: err.Error()}, nil
	}

	tool, haveTool := x.Registry.Get(pc.Tool)
	c := Call{Agent: id, Tool: pc.Tool, Action: pc.Action, Args: pc.Args}

	// Overlay trusted, tool-derived facts so the agent cannot hide what the
	// action really does. Policy evaluates (and fingerprints) this merged call.
	pcPolicy := pc
	if haveTool {
		if rs, ok := tool.(ArgResolver); ok {
			pcPolicy.Args = overlay(pc.Args, safeResolve(ctx, rs, c))
		}
	}

	// registry-level gate (coarse); policy is still consulted for the audit trail
	var gate string
	switch {
	case !haveTool:
		gate = fmt.Sprintf("unknown tool %q", pc.Tool)
	case !x.Registry.Permitted(id.AgentID, pc.Tool, pc.Action):
		gate = fmt.Sprintf("agent %q has no access to %s.%s", id.AgentID, pc.Tool, pc.Action)
	case !hasAction(tool, pc.Action):
		gate = fmt.Sprintf("tool %q has no action %q", pc.Tool, pc.Action)
	}

	var d policy.Decision
	switch {
	case gate != "":
		d = x.Policy.Evaluate(ctx, id, pcPolicy)
		d = policy.Decision{Deny: true, RuleID: "registry.denied", Reason: gate, Trace: append(d.Trace, d.RuleID)}
	case tok != nil:
		d = x.Policy.EvaluateApproved(ctx, id, pcPolicy, *tok)
	default:
		d = x.Policy.EvaluateAndReserve(ctx, id, pcPolicy)
	}

	switch {
	case d.Deny:
		if aerr := x.Audit.Record(ctx, entry("decision", pcPolicy, d, StatusDenied)); aerr != nil {
			return Outcome{}, fmt.Errorf("tools: audit failed: %w", aerr)
		}
		return Outcome{Status: StatusDenied, Decision: d, Error: d.Reason}, nil
	case d.RequireApproval:
		if aerr := x.Audit.Record(ctx, entry("decision", pcPolicy, d, StatusPendingApproval)); aerr != nil {
			return Outcome{}, fmt.Errorf("tools: audit failed: %w", aerr)
		}
		return Outcome{Status: StatusPendingApproval, Decision: d, Pending: x.pending(id, pcPolicy, d)}, nil
	case !d.Allow:
		d = policy.Decision{Deny: true, RuleID: "policy.invalid", Reason: "policy returned no verdict"}
		_ = x.Audit.Record(ctx, entry("decision", pcPolicy, d, StatusDenied))
		return Outcome{Status: StatusDenied, Decision: d, Error: d.Reason}, nil
	}

	// allowed: audit BEFORE executing (fail closed), then audit the result.
	if aerr := x.Audit.Record(ctx, entry("decision", pcPolicy, d, "authorized")); aerr != nil {
		return Outcome{}, fmt.Errorf("tools: audit failed, action not executed: %w", aerr)
	}
	res, xerr := safeExecute(ctx, tool, c)
	if xerr != nil {
		e := entry("result", pcPolicy, d, StatusFailed)
		e.Error = xerr.Error()
		if aerr := x.Audit.Record(ctx, e); aerr != nil {
			return Outcome{Status: StatusFailed, Decision: d, Error: xerr.Error()}, fmt.Errorf("tools: audit failed: %w", aerr)
		}
		return Outcome{Status: StatusFailed, Decision: d, Error: xerr.Error()}, nil
	}
	e := entry("result", pcPolicy, d, StatusExecuted)
	e.Summary = truncate(res.Summary, 300)
	if aerr := x.Audit.Record(ctx, e); aerr != nil {
		return Outcome{Status: StatusExecuted, Decision: d, Result: &res}, fmt.Errorf("tools: executed but result audit failed: %w", aerr)
	}
	return Outcome{Status: StatusExecuted, Decision: d, Result: &res}, nil
}

func (x *Executor) pending(id policy.AgentIdentity, pc policy.ToolCall, d policy.Decision) *PendingApproval {
	risk := "medium"
	if strings.EqualFold(pc.Risk, "high") || strings.HasPrefix(d.RuleID, "org.always_approve") {
		risk = "high"
	} else if strings.EqualFold(pc.Risk, "low") && !strings.HasPrefix(d.RuleID, "org.") {
		risk = "low"
	}
	return &PendingApproval{
		Fingerprint: policy.Fingerprint(id, pc), Tool: pc.Tool, Action: pc.Action, Args: RedactArgs(pc.Args), Risk: risk,
		Title:   fmt.Sprintf("%s: %s.%s", id.AgentID, pc.Tool, pc.Action),
		Details: d.Reason,
	}
}

func hasAction(t Tool, action string) bool {
	for _, a := range t.Actions() {
		if policy.NormalizeName(a.Name) == action {
			return true
		}
	}
	return false
}

func safeExecute(ctx context.Context, t Tool, c Call) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return t.Execute(ctx, c)
}

func safeResolve(ctx context.Context, r ArgResolver, c Call) (m map[string]any) {
	defer func() {
		if recover() != nil {
			m = nil
		}
	}()
	return r.ResolveArgs(ctx, c)
}

// overlay returns base with resolved facts layered on top (base is not mutated).
func overlay(base, resolved map[string]any) map[string]any {
	if len(resolved) == 0 {
		return base
	}
	out := make(map[string]any, len(base)+len(resolved))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range resolved {
		out[k] = v
	}
	return out
}

var secretKey = regexp.MustCompile(`(?i)(pass(word)?|token|secret|api[_-]?key|authorization|credential|iban|card)`)

// RedactArgs copies args masking secret-looking keys and truncating long text.
func RedactArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	return redactMap(args, 0)
}

func redactMap(m map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if secretKey.MatchString(k) {
			out[k] = "[redacted]"
			continue
		}
		out[k] = redactVal(v, depth)
	}
	return out
}

func redactVal(v any, depth int) any {
	if depth > 5 {
		return "[truncated]"
	}
	switch t := v.(type) {
	case string:
		return truncate(t, 200)
	case map[string]any:
		return redactMap(t, depth+1)
	case []any:
		out := make([]any, 0, len(t))
		for i, x := range t {
			if i >= 20 {
				break
			}
			out = append(out, redactVal(x, depth+1))
		}
		return out
	}
	return v
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// MemoryAudit is a simple in-memory AuditSink (tests, dev, demo).
type MemoryAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
	fail    error
}

// NewMemoryAudit creates a MemoryAudit.
func NewMemoryAudit() *MemoryAudit { return &MemoryAudit{} }

// SetFail makes Record return err (nil restores normal behaviour). For tests.
func (m *MemoryAudit) SetFail(err error) {
	m.mu.Lock()
	m.fail = err
	m.mu.Unlock()
}

// Record implements AuditSink.
func (m *MemoryAudit) Record(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.entries = append(m.entries, e)
	return nil
}

// Entries returns a copy of the recorded entries.
func (m *MemoryAudit) Entries() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEntry(nil), m.entries...)
}
