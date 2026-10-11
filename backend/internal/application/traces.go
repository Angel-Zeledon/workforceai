package application

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"aiworkforce/backend/internal/sanitize"
)

// Bounds of a stored trace. Every text is cut and flagged `truncated`; nothing
// grows with the size of the prompt or of the model answer.
const (
	maxTraceSystemRunes   = 4000
	maxTraceTextRunes     = 8000
	maxTraceUntrusted     = 4000
	maxTraceSegments      = 24
	maxTraceRationale     = 1000
	maxTraceSummary       = 400
	maxTraceError         = 400
	maxTraceEvents        = 40
	maxTraceTools         = 20
	DefaultTraceRetention = 30
)

// Tracer records the thought audit: one Trace per runtime call of an agent. It
// never blocks or fails a run, redacts everything it stores (secrets and PII)
// and honors the organization flag store_prompts.
type Tracer struct {
	Store     V2Store
	Suspects  *sanitize.Suspects
	Log       *slog.Logger
	Retention time.Duration // traces older than this are purged (TRACE_RETENTION_DAYS)
	Now       func() time.Time
}

func (t *Tracer) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now().UTC()
}

func (t *Tracer) redact(s string) string {
	return sanitize.RedactPII(sanitize.RedactSecrets(s, t.Suspects))
}

func cutRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]) + "…", true
}

// StorePrompts reports whether the organization keeps prompts (default true).
func (t *Tracer) StorePrompts(ctx context.Context, org string) bool {
	on, err := t.Store.GetTraceStorePrompts(ctx, org)
	return err != nil || on
}

var untrustedBlock = regexp.MustCompile(`(?s)=====\[DATOS NO CONFIABLES: (.*?) - INICIO\]=====\n(.*?)\n=====\[DATOS NO CONFIABLES: .*? - FIN\]=====`)

// SplitPrompt turns the user prompt into plain-text and delimited untrusted
// segments, redacted and bounded.
func (t *Tracer) SplitPrompt(user string) ([]PromptSegment, bool) {
	var segs []PromptSegment
	trunc := false
	add := func(kind, label, text string, limit int) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(segs) >= maxTraceSegments {
			trunc = true
			return
		}
		text, cut := cutRunes(t.redact(text), limit)
		trunc = trunc || cut
		segs = append(segs, PromptSegment{Kind: kind, Label: label, Text: text, Truncated: cut})
	}
	last := 0
	for _, m := range untrustedBlock.FindAllStringSubmatchIndex(user, -1) {
		add("text", "", user[last:m[0]], maxTraceTextRunes)
		add("untrusted", user[m[2]:m[3]], user[m[4]:m[5]], maxTraceUntrusted)
		last = m[1]
	}
	add("text", "", user[last:], maxTraceTextRunes)
	return segs, trunc
}

// RunCall is what the orchestrator tells the tracer about one runtime call.
type RunCall struct {
	AgentID, TaskID, RequestID, Kind string
	In                               RunTaskRequest
	Resp                             *RunTaskResponse
	Err                              error
	Latency                          time.Duration
}

// Record stores the trace of a call. Errors are logged, never returned.
func (t *Tracer) Record(ctx context.Context, org string, c RunCall) {
	tr := Trace{ID: newID(), AgentID: c.AgentID, TaskID: c.TaskID, RequestID: c.RequestID, Kind: c.Kind, CreatedAt: t.now(),
		Status: "ok", ToolRequests: []TraceTool{}, Events: []TraceEvent{}, SteerNotesApplied: len(c.In.HumanGuidance), LatencyMS: int(c.Latency.Milliseconds())}
	if c.Err != nil {
		tr.Status = "error"
		tr.Error, _ = cutRunes(t.redact(c.Err.Error()), maxTraceError)
	}
	if r := c.Resp; r != nil {
		tr.Provider, tr.Model = r.Usage.Provider, r.Usage.Model
		tr.InputTokens, tr.OutputTokens, tr.CostUSD = r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CostUSD
		if tr.LatencyMS == 0 {
			tr.LatencyMS = r.Usage.DurationMS
		}
		var cut bool
		tr.Rationale, cut = cutRunes(t.redact(r.Output.Summary), maxTraceRationale)
		tr.Truncated = tr.Truncated || cut
		sum, _ := cutRunes(t.redact(r.Output.Summary), maxTraceSummary)
		tr.OutputSummary = TraceOutput{Summary: sum, Confidence: r.Output.Confidence, Findings: len(r.Output.Findings), Recommendations: len(r.Output.Recommendations)}
		for i, q := range r.ToolRequests { // arguments are never stored: only which tool and action
			if i >= maxTraceTools {
				tr.Truncated = true
				break
			}
			tr.ToolRequests = append(tr.ToolRequests, TraceTool{Tool: q.Tool, Action: q.Action, Risk: strings.ToLower(q.Risk)})
		}
		if r.Trace != nil && t.StorePrompts(ctx, org) {
			sys, cs := cutRunes(t.redact(r.Trace.System), maxTraceSystemRunes)
			segs, ct := t.SplitPrompt(r.Trace.User)
			tr.Prompt = &TracePrompt{System: sys, Segments: segs}
			tr.PromptStored, tr.Truncated = true, tr.Truncated || cs || ct
		}
	}
	if err := t.Store.AddTrace(ctx, org, tr); err != nil && t.Log != nil {
		t.Log.Warn("trace: store", "err", err)
	}
}

// Event appends an event to the latest trace of a task (metadata only).
func (t *Tracer) Event(ctx context.Context, org, taskID, typ string, data map[string]any) {
	clean := map[string]any{}
	for k, v := range data {
		if s, ok := v.(string); ok {
			s, _ = cutRunes(t.redact(s), 300)
			clean[k] = s
		} else {
			clean[k] = v
		}
	}
	if _, err := t.Store.AppendTraceEvent(ctx, org, taskID, TraceEvent{TS: t.now(), Type: typ, Data: clean}); err != nil && t.Log != nil {
		t.Log.Warn("trace: event", "err", err)
	}
}

// Purge deletes the traces older than the retention window of one organization.
func (t *Tracer) Purge(ctx context.Context, org string) (int, error) {
	ret := t.Retention
	if ret <= 0 {
		ret = DefaultTraceRetention * 24 * time.Hour
	}
	return t.Store.PurgeTraces(ctx, org, t.now().Add(-ret))
}

// MergeDecisions fills the tool requests of a trace with the policy decisions
// and approval outcomes found in its events.
func MergeDecisions(tr *Trace) {
	for _, e := range tr.Events {
		tool, _ := e.Data["tool"].(string)
		action, _ := e.Data["action"].(string)
		for i := range tr.ToolRequests {
			q := &tr.ToolRequests[i]
			if q.Tool != tool || q.Action != action {
				continue
			}
			switch e.Type {
			case "tool_decision":
				q.Decision, _ = e.Data["effect"].(string)
				q.RuleID, _ = e.Data["rule_id"].(string)
				q.Reason, _ = e.Data["reason"].(string)
			case "approval":
				q.ApprovalStatus, _ = e.Data["status"].(string)
			}
		}
	}
}
