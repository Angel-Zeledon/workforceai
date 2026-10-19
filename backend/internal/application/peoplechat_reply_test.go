package application_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func TestPeopleChatReplyUsesTheChatRuntimeAndCreatesNoWork(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "general", primary("legal")))
	h := newChat(t, rt, nil)
	out, err := h.orch.PeopleChatReply(context.Background(), "legal", "@leo can you review the contract?", []application.ChatHistoryItem{{From: "user", Text: "hola"}})
	if err != nil || out != "dice legal" {
		t.Fatalf("reply = %q, %v", out, err)
	}
	if len(rt.replies) != 1 || rt.replies[0].Intent != domain.IntentQuestion || len(rt.replies[0].History) != 1 || rt.replies[0].Agent.ID != "legal" {
		t.Fatalf("runtime request = %+v", rt.replies)
	}
	h.noWork()
}

func TestPeopleChatReplyFallsBackToACannedLine(t *testing.T) {
	// a runtime without chat support (simulation) still answers, with no cost
	h := newChat(t, &fakeRuntime{plan: proposalPlan()}, nil)
	out, err := h.orch.PeopleChatReply(context.Background(), "legal", "@leo hola", nil)
	if err != nil || out == "" {
		t.Fatalf("canned reply = %q, %v", out, err)
	}
}

func TestPeopleChatReplyRespectsPauseAndUnknownAgent(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("question", "general", primary("legal"))), nil)
	h.orch.SetConnections(guardFake{deny: map[string]string{"legal": "agent_paused"}}, nil, nil)
	if _, err := h.orch.PeopleChatReply(context.Background(), "legal", "hola", nil); !errors.Is(err, application.ErrNoReply) {
		t.Fatalf("a paused agent does not answer: %v", err)
	}
	if _, err := h.orch.PeopleChatReply(context.Background(), "ghost", "hola", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}
