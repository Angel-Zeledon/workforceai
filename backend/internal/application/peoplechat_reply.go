package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// ErrNoReply: the agent will not answer now (paused, kill switch). Not a failure worth surfacing.
var ErrNoReply = errors.New("agent will not reply")

// PeopleChatReply is ONE short chat reply of an agent that was @mentioned in a people channel
// (docs/architecture/presence.md). It reuses the chat machinery of the orchestrator: the same guards (kill
// switch, agent pause), the same budget reservation and usage ledger, the same runtime call (POST /v1/chat-reply)
// and the same canned fallback, so it costs nothing in simulation and when the runtime is down. It never creates a
// request or a task, and the history it receives is DATA (the runtime treats it as untrusted messages).
func (o *Orchestrator) PeopleChatReply(ctx context.Context, agentID, text string, history []ChatHistoryItem) (string, error) {
	org := o.org(ctx)
	agents, err := o.store.ListAgents(ctx, org)
	if err != nil {
		return "", err
	}
	var agent domain.Agent
	found := false
	for _, a := range agents {
		if a.ID == agentID {
			agent, found = a, true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%w: agent %q", domain.ErrNotFound, agentID)
	}
	if v := o.chatAdmit(ctx, agent.ID); !v.Allowed {
		return "", ErrNoReply
	}
	style := o.loadStyle(ctx)
	loc := chatLocale(style)
	req := ChatReplyRequest{
		Agent: chatAgent(agent, style.Locale), Text: truncate(strings.TrimSpace(text), 800), Conversation: domain.ChatAgentPrefix + agent.ID,
		Intent: domain.IntentQuestion, Topic: "general", ResponderRole: domain.RolePrimary,
		Agents: o.routeAgents(agents, style.Locale), History: history, Locale: style.Locale, Tone: style.ToneFor(agent.ID),
	}
	res, ex := o.chatReserve(ctx, agent.ID)
	if ex != nil {
		return "", ex
	}
	var resp ChatReplyResponse
	if cr, ok := o.rt.(ChatRuntime); ok {
		cctx, cancel := context.WithTimeout(ctx, o.chatTimeout())
		resp, err = cr.ChatReply(cctx, req)
		cancel()
		if err == nil && strings.TrimSpace(resp.Text) == "" {
			err = errors.New("empty reply")
		}
	} else {
		err = errors.New("runtime without chat support")
	}
	if err != nil {
		res.Release()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		o.log.Warn("people chat reply failed, using canned reply", "agent", agent.ID, "err", err)
		out := fallbackReply(loc, req, agent)
		if out == "" {
			return "", ErrNoReply
		}
		return out, nil
	}
	o.chatRecordUsage(ctx, agent.ID, resp.Usage, res)
	return strings.TrimSpace(resp.Text), nil
}
