package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Redirects, handoffs, reassignments and agent-voiced limits of the chat layer
// (docs/architecture/chat-routing.md, "Out of competence and limits").

// handoffKey is where a pending redirect of a conversation is remembered. The
// memory of the assistant is only a holder: one pending redirect per conversation.
func handoffKey(conv string) string { return "handoff:" + conv }

func (o *Orchestrator) rememberHandoff(ctx context.Context, t *chatTurn, h handoff) {
	raw, err := json.Marshal(h)
	if err != nil {
		return
	}
	if err := o.store.SetMemory(ctx, o.org(ctx), assistantID, domain.Memory{Scope: "chat", Key: handoffKey(t.conv), Value: string(raw)}); err != nil {
		o.log.Warn("remember handoff", "err", err)
	}
}

// takeHandoff returns (and clears) the pending redirect of the conversation. A
// redirect only lives until the next message, accepted or not.
func (o *Orchestrator) takeHandoff(ctx context.Context, t *chatTurn) *handoff {
	mem, err := o.store.ListMemory(ctx, o.org(ctx), assistantID)
	if err != nil {
		return nil
	}
	for _, m := range mem {
		if m.Scope != "chat" || m.Key != handoffKey(t.conv) || m.Value == "" {
			continue
		}
		_ = o.store.SetMemory(ctx, o.org(ctx), assistantID, domain.Memory{Scope: "chat", Key: m.Key, Value: ""})
		var h handoff
		if json.Unmarshal([]byte(m.Value), &h) != nil || h.To == "" {
			return nil
		}
		if _, ok := t.byID[h.To]; !ok {
			return nil
		}
		if _, ok := t.byID[h.From]; !ok {
			return nil
		}
		return &h
	}
	return nil
}

// chatHandoff runs an accepted redirect: the first agent says goodbye in its own
// voice and the colleague takes the turn with the original question.
func (o *Orchestrator) chatHandoff(ctx context.Context, t *chatTurn, route RouteResponse) {
	h := t.handoff
	from, to := t.byID[h.From], t.byID[h.To]
	req := o.chatReplyRequest(t, from, RouteResponse{Intent: domain.IntentQuestion, Topic: "general"},
		RouteResponder{AgentID: from.ID, Role: domain.RolePrimary}, 0, nil)
	req.Text, req.Handoff, req.ConsultTo = h.Text, true, to.ID
	resp, res := o.chatSpeak(ctx, t, from, req, domain.MsgChat, "user", 0)
	if res == speakStop {
		return
	}
	var prior []ChatHistoryItem
	if res == speakDone {
		prior = append(prior, ChatHistoryItem{From: from.ID, Text: resp.Text})
	}
	req2 := o.chatReplyRequest(t, to, RouteResponse{Intent: domain.IntentQuestion, Topic: "general"},
		route.Responders[0], 1, prior)
	req2.Text, req2.ConsultTo = h.Text, ""
	o.chatSpeak(ctx, t, to, req2, domain.MsgChat, "user", o.cfg.ChatStagger)
}

// chatLimitSay lets an agent say "I can't do that" in its own voice (kill
// switch, pause, budget, read-only, no connection). The runtime scripts these
// lines in every mode (no LLM call, no cost). False = the runtime could not
// provide one and the caller posts a plain system notice.
func (o *Orchestrator) chatLimitSay(ctx context.Context, t *chatTurn, agent domain.Agent, limit string) bool {
	cr, ok := o.rt.(ChatRuntime)
	if !ok {
		return false
	}
	req := ChatReplyRequest{
		Agent: ChatAgent{ID: agent.ID, Role: agent.Role, Title: agent.Title, Name: agent.Name, Persona: agent.Persona},
		Text:  t.text, Conversation: t.conv, Intent: domain.IntentTask, Topic: "general", ResponderRole: domain.RolePrimary,
		Agents: o.routeAgents(t.agents), History: t.history, Limit: limit, Locale: t.style.Locale, Tone: t.style.ToneFor(agent.ID),
	}
	cctx, cancel := context.WithTimeout(ctx, o.chatTimeout())
	resp, err := cr.ChatReply(cctx, req)
	cancel()
	text := strings.TrimSpace(resp.Text)
	if err != nil || text == "" {
		return false
	}
	reply := t.userMsgID
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: t.conv, From: agent.ID, To: "user", Kind: domain.MsgChat, Text: text,
		TS: time.Now().UTC(), TurnID: t.id, ReplyTo: &reply}, "")
	return true
}

// limitOf maps a guard code to the limit kind the runtime knows.
func limitOf(code string) string {
	switch code {
	case "kill_switch_active", "controls_unavailable":
		return "kill_switch"
	case "agent_paused":
		return "paused"
	case "read_only_mode":
		return "read_only"
	}
	return "paused"
}

// chatReassign moves a task of a chat-born request away from the agent that
// declined it (clearly outside their role) to the colleague who owns the topic,
// and says so in assigned_reason.
func (o *Orchestrator) chatReassign(ctx context.Context, rs *run, agent string, pt PlannedTask) (string, string) {
	l := rs.chat
	if l == nil || l.reassignFrom == "" || agent != l.reassignFrom {
		return agent, ""
	}
	from, to := rs.agents[agent], rs.agents[l.reassignTo]
	if to.ID == "" || from.ID == "" {
		return agent, ""
	}
	if topicScores(normText(pt.Title + " " + pt.Description))[from.Role] > 0 {
		return agent, "" // this part really is theirs
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: from.ID, Action: "chat.task_reassigned", Entity: "request", EntityID: rs.req.ID,
		Details: map[string]any{"task": pt.Key, "from": from.ID, "to": to.ID}})
	return to.ID, fmt.Sprintf(chatText(l.loc, "reassigned"), from.Name, to.Name, defaultAssignReason(l.loc, to))
}
