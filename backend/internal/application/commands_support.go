package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Support for the slash commands (docs/architecture/commands.md): the context
// of a conversation (clear / compact), the summarize and context-sizes calls
// of the runtime and a few helpers the executors in internal/api use. Nothing
// here depends on the model behind the runtime.

// ConvContext is what /clear and /compact store about a conversation: messages
// at or before Cutoff() are not sent to the model any more (they stay in the
// history), and Summary (when set) is sent instead of them.
type ConvContext struct {
	ConversationID string    `json:"conversation_id"`
	Epoch          int       `json:"epoch"`
	ClearedAt      time.Time `json:"cleared_at"`
	Summary        string    `json:"summary"`
	SummaryUpto    time.Time `json:"summary_upto"`
	SummaryTokens  int       `json:"summary_tokens"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Cutoff is the time up to which the conversation is hidden from the model.
func (c ConvContext) Cutoff() time.Time {
	if c.SummaryUpto.After(c.ClearedAt) {
		return c.SummaryUpto
	}
	return c.ClearedAt
}

// ConvContextStore persists ConvContext (Postgres migration 600; memory store).
type ConvContextStore interface {
	GetConvContext(ctx context.Context, org, conversation string) (ConvContext, bool, error)
	PutConvContext(ctx context.Context, org string, c ConvContext) error
}

// MemoryDeleter is the optional Store capability behind /memory delete|forget.
type MemoryDeleter interface {
	DeleteMemory(ctx context.Context, orgID, agentID, scope, key string) error
}

// SetConvContext installs the store that /clear and /compact write; the chat
// history of every turn then honors it. nil removes it.
func (o *Orchestrator) SetConvContext(s ConvContextStore) { o.convCtx = s }

// ConvContextOf returns the stored context of a conversation (zero value when none).
func (o *Orchestrator) ConvContextOf(ctx context.Context, conversation string) ConvContext {
	if o.convCtx == nil {
		return ConvContext{ConversationID: conversation}
	}
	c, ok, err := o.convCtx.GetConvContext(ctx, o.org(ctx), conversation)
	if err != nil || !ok {
		return ConvContext{ConversationID: conversation}
	}
	return c
}

// SaveConvContext stores the context of a conversation.
func (o *Orchestrator) SaveConvContext(ctx context.Context, c ConvContext) error {
	if o.convCtx == nil {
		return fmt.Errorf("%w: conversation context is not available", domain.ErrInvalid)
	}
	c.UpdatedAt = time.Now().UTC()
	return o.convCtx.PutConvContext(ctx, o.org(ctx), c)
}

// HasConvContext says whether /clear and /compact can be stored.
func (o *Orchestrator) HasConvContext() bool { return o.convCtx != nil }

// summaryHistoryChars bounds the summary that replaces the old messages in a prompt.
const summaryHistoryChars = 3000

// applyConvContext hides what /clear or /compact retired and puts the summary first.
func (o *Orchestrator) applyConvContext(ctx context.Context, conv string, msgs []domain.Message) ([]domain.Message, *ChatHistoryItem) {
	if o.convCtx == nil {
		return msgs, nil
	}
	c, ok, err := o.convCtx.GetConvContext(ctx, o.org(ctx), conv)
	if err != nil || !ok {
		return msgs, nil
	}
	cut := c.Cutoff()
	if !cut.IsZero() {
		kept := msgs[:0:0]
		for _, m := range msgs {
			if m.TS.After(cut) {
				kept = append(kept, m)
			}
		}
		msgs = kept
	}
	if strings.TrimSpace(c.Summary) == "" {
		return msgs, nil
	}
	return msgs, &ChatHistoryItem{From: "system", Text: truncate(c.Summary, summaryHistoryChars)}
}

// ---- runtime ports (optional, type-asserted like ChatRuntime) ----

// SummarizeRequest asks the runtime for a summary. Mode: compact | meeting | daily.
type SummarizeRequest struct {
	Mode            string            `json:"mode"`
	Messages        []ChatHistoryItem `json:"messages"`
	PreviousSummary string            `json:"previous_summary,omitempty"`
	Topic           string            `json:"topic,omitempty"`
	Participants    []string          `json:"participants,omitempty"`
	Locale          string            `json:"locale,omitempty"`
	Tone            string            `json:"tone,omitempty"`
}

// SummarizeResponse is the summary and what it cost.
type SummarizeResponse struct {
	Text  string `json:"text"`
	Usage Usage  `json:"usage"`
}

// ContextSegment is one part of what an agent receives.
type ContextSegment struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
}

// ContextSizesResponse is POST /v1/context-sizes.
type ContextSizesResponse struct {
	Segments    []ContextSegment `json:"segments"`
	TotalTokens int              `json:"total_tokens"`
}

// SummaryRuntime is the runtime capability behind POST /v1/summarize.
type SummaryRuntime interface {
	Summarize(ctx context.Context, in SummarizeRequest) (SummarizeResponse, error)
}

// ContextSizeRuntime is the runtime capability behind POST /v1/context-sizes.
type ContextSizeRuntime interface {
	ContextSizes(ctx context.Context, in RunTaskRequest) (ContextSizesResponse, error)
}

// SummaryResult is a summary with its provenance.
type SummaryResult struct {
	Text   string
	Usage  Usage
	Source string // "runtime" | "local" (the runtime has no summarizer or failed: extractive summary, no cost)
}

// Summarize summarizes through the active LLM like any other chat call: budget
// reservation first (an *Exceeded error when there is no money), usage in the
// ledger. When the runtime cannot summarize it falls back to a local extractive
// summary, so /compact works even then.
func (o *Orchestrator) Summarize(ctx context.Context, agentID string, in SummarizeRequest) (SummaryResult, error) {
	if agentID == "" {
		agentID = assistantID
	}
	if in.Locale == "" {
		in.Locale = o.loadStyle(ctx).Locale
	}
	if in.Tone == "" {
		in.Tone = o.loadStyle(ctx).ToneFor(agentID)
	}
	sr, ok := o.rt.(SummaryRuntime)
	if !ok {
		return SummaryResult{Text: LocalSummary(in), Source: "local"}, nil
	}
	res, ex := o.chatReserve(ctx, agentID, UsageSummarize, in)
	if ex != nil {
		return SummaryResult{}, ex
	}
	cctx, cancel := context.WithTimeout(ctx, 4*o.chatTimeout())
	defer cancel()
	resp, err := sr.Summarize(cctx, in)
	if err == nil && strings.TrimSpace(resp.Text) == "" {
		err = errors.New("empty summary")
	}
	if err != nil {
		res.Release()
		if ctx.Err() != nil {
			return SummaryResult{}, ctx.Err()
		}
		o.log.Warn("summarize failed, using the local summary", "err", err)
		o.rec.Audit(ctx, domain.AuditLog{Actor: agentID, Action: "command.summarize_fallback", Entity: "conversation", EntityID: "summary",
			Details: map[string]any{"mode": in.Mode, "error": truncate(err.Error(), 200)}})
		return SummaryResult{Text: LocalSummary(in), Source: "local"}, nil
	}
	o.chatRecordUsage(ctx, agentID, resp.Usage, res)
	return SummaryResult{Text: strings.TrimSpace(resp.Text), Usage: resp.Usage, Source: "runtime"}, nil
}

// LocalSummary is the deterministic extractive summary used when the runtime
// cannot summarize (and by tests). It keeps the end of the conversation.
func LocalSummary(in SummarizeRequest) string {
	en := in.Locale == "en"
	var b strings.Builder
	switch in.Mode {
	case "meeting":
		if en {
			fmt.Fprintf(&b, "Meeting conclusion: %s\n", in.Topic)
		} else {
			fmt.Fprintf(&b, "Conclusión de la reunión: %s\n", in.Topic)
		}
	case "daily":
		if en {
			b.WriteString("Summary of the day\n")
		} else {
			b.WriteString("Resumen del día\n")
		}
	default:
		if en {
			fmt.Fprintf(&b, "Conversation summary (%d messages)\n", len(in.Messages))
		} else {
			fmt.Fprintf(&b, "Resumen de la conversación (%d mensajes)\n", len(in.Messages))
		}
	}
	if in.PreviousSummary != "" {
		b.WriteString(map[bool]string{true: "Earlier: ", false: "Antes: "}[en] + truncate(in.PreviousSummary, 400) + "\n")
	}
	msgs := in.Messages
	if len(msgs) > 12 {
		msgs = msgs[len(msgs)-12:]
	}
	for _, m := range msgs {
		fmt.Fprintf(&b, "- %s: %s\n", m.From, truncate(strings.Join(strings.Fields(m.Text), " "), 140))
	}
	return strings.TrimSpace(b.String())
}

// ApproxTokens is the rough size of a text (4 characters per token), the same rule the runtime uses.
func ApproxTokens(s string) int { return (len([]rune(s)) + 3) / 4 }

// PreviewRunRequest builds the request the agent would get for a task right now
// (profile, skills, memory, workspace), with the same builder as a real run.
func (o *Orchestrator) PreviewRunRequest(ctx context.Context, agentID, text string) (RunTaskRequest, error) {
	agent, err := o.store.GetAgent(ctx, o.org(ctx), agentID)
	if err != nil {
		return RunTaskRequest{}, err
	}
	rs := &run{req: domain.Request{Text: text}, style: o.loadStyle(ctx), done: map[string]domain.Task{}, agents: map[string]domain.Agent{agent.ID: agent}}
	t := domain.Task{ID: "preview", Title: "(preview)", Description: text, AgentID: agentID}
	in := o.buildRunRequest(ctx, rs, t, agent)
	if p := o.v2.profiles; p != nil { // the work profile is part of what the agent receives
		if rp, ok := p.RunProfileOf(ctx, agentID); ok {
			in.Agent.Instructions, in.Agent.Creativity = rp.Instructions, rp.Creativity
		}
	}
	return in, nil
}

// ContextSizes asks the runtime for the segment sizes; ok is false when it cannot.
func (o *Orchestrator) ContextSizes(ctx context.Context, in RunTaskRequest) (ContextSizesResponse, bool) {
	cs, ok := o.rt.(ContextSizeRuntime)
	if !ok {
		return ContextSizesResponse{}, false
	}
	cctx, cancel := context.WithTimeout(ctx, o.chatTimeout())
	defer cancel()
	out, err := cs.ContextSizes(cctx, in)
	if err != nil {
		o.log.Warn("context sizes failed", "err", err)
		return ContextSizesResponse{}, false
	}
	return out, true
}

// PostSystemNotice stores a system line in a chat conversation (creating it when needed) and publishes it.
func (o *Orchestrator) PostSystemNotice(ctx context.Context, conversation, text string) error {
	if !IsChatConversation(conversation) || conversation == domain.ChatAgentPrefix {
		return fmt.Errorf("%w: conversation must be %q or %q", domain.ErrInvalid, domain.ChatOffice, domain.ChatAgentPrefix+"<agent id>")
	}
	agents, err := o.store.ListAgents(ctx, o.org(ctx))
	if err != nil {
		return err
	}
	t := &chatTurn{conv: conversation, agents: agents, byID: map[string]domain.Agent{}, style: o.loadStyle(ctx)}
	t.loc = chatLocale(t.style)
	for _, a := range agents {
		t.byID[a.ID] = a
	}
	if id, ok := strings.CutPrefix(conversation, domain.ChatAgentPrefix); ok {
		if _, known := t.byID[id]; !known {
			return fmt.Errorf("%w: agent %q", domain.ErrNotFound, id)
		}
		t.direct = id
	}
	if err := o.ensureChatConv(ctx, t); err != nil {
		return err
	}
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: conversation, From: "system", To: "user", Kind: domain.MsgChat,
		Text: text, TS: time.Now().UTC()}, "")
	return nil
}

// ChatMessagesSince returns the conversation's messages newer than since (oldest first), at most limit (the newest ones).
func (o *Orchestrator) ChatMessagesSince(ctx context.Context, conversation string, since time.Time, limit int) ([]domain.Message, error) {
	var msgs []domain.Message
	var err error
	if p, ok := o.store.(MessagePager); ok {
		msgs, _, err = p.ListMessagesPage(ctx, o.org(ctx), conversation, "", limit+50)
	} else {
		msgs, err = o.store.ListMessages(ctx, o.org(ctx), conversation)
	}
	if err != nil {
		return nil, err
	}
	out := msgs[:0:0]
	for _, m := range msgs {
		if m.TS.After(since) {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// DeleteMemory removes one memory entry when the store supports it.
func (o *Orchestrator) DeleteMemory(ctx context.Context, agentID, scope, key string) error {
	d, ok := o.store.(MemoryDeleter)
	if !ok {
		return fmt.Errorf("%w: this store cannot delete memories", domain.ErrInvalid)
	}
	return d.DeleteMemory(ctx, o.org(ctx), agentID, scope, key)
}
