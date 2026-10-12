package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
)

// Slash commands support: /clear and /compact decide what the model still sees, and
// the summary goes through the same budget and ledger as every other chat call.

type summaryRT struct {
	*chatRT
	got  []application.SummarizeRequest
	text string
	err  error
}

func (s *summaryRT) Summarize(_ context.Context, in application.SummarizeRequest) (application.SummarizeResponse, error) {
	s.got = append(s.got, in)
	if s.err != nil {
		return application.SummarizeResponse{}, s.err
	}
	return application.SummarizeResponse{Text: s.text, Usage: application.Usage{Model: "m", InputTokens: 900, OutputTokens: 120, CostUSD: 0.002}}, nil
}

func TestConvContextHidesWhatWasClearedAndSendsTheSummary(t *testing.T) {
	rt := &summaryRT{chatRT: newChatRuntime(fixedRoute("question", "general", primary("assistant"))), text: "RESUMEN"}
	h := newChat(t, rt, nil)
	h.orch.SetConvContext(h.store)
	h.say("office", "primera")
	h.say("office", "segunda")
	time.Sleep(5 * time.Millisecond)

	// /clear: the model stops seeing the older messages; they stay stored.
	cc := h.orch.ConvContextOf(context.Background(), "office")
	cc.ConversationID, cc.Epoch, cc.ClearedAt = "office", 1, time.Now().UTC()
	if err := h.orch.SaveConvContext(context.Background(), cc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	h.say("office", "tercera")
	rt.mu.Lock()
	hist := rt.routes[len(rt.routes)-1].History
	rt.mu.Unlock()
	if len(hist) != 0 {
		t.Fatalf("after /clear the model sees %+v", hist)
	}
	if n := len(h.messages("office")); n < 6 {
		t.Fatalf("the messages must stay in the history, have %d", n)
	}

	// /compact: the summary replaces what came before and later turns send it first.
	cc = h.orch.ConvContextOf(context.Background(), "office")
	cc.Summary, cc.SummaryUpto = "RESUMEN DE LA TERCERA", time.Now().UTC()
	if err := h.orch.SaveConvContext(context.Background(), cc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	h.say("office", "cuarta")
	rt.mu.Lock()
	hist = rt.routes[len(rt.routes)-1].History
	rt.mu.Unlock()
	if len(hist) != 1 || hist[0].From != "system" || hist[0].Text != "RESUMEN DE LA TERCERA" {
		t.Fatalf("after /compact the model sees %+v", hist)
	}
	h.say("office", "quinta")
	rt.mu.Lock()
	hist = rt.routes[len(rt.routes)-1].History
	rt.mu.Unlock()
	if len(hist) < 2 || hist[0].Text != "RESUMEN DE LA TERCERA" || hist[1].Text != "cuarta" {
		t.Fatalf("the summary stays first and newer messages follow: %+v", hist)
	}
}

func TestSummarizeUsesBudgetAndLedgerAndFallsBackLocally(t *testing.T) {
	rt := &summaryRT{chatRT: newChatRuntime(fixedRoute("question", "general", primary("assistant"))), text: "  resumen  "}
	h := newChat(t, rt, nil)
	ctx := context.Background()
	in := application.SummarizeRequest{Mode: "compact", Locale: "en", Messages: []application.ChatHistoryItem{{From: "user", Text: "hello"}, {From: "sales", Text: "hi"}}}
	res, err := h.orch.Summarize(ctx, "sales", in)
	if err != nil || res.Text != "resumen" || res.Source != "runtime" || res.Usage.CostUSD != 0.002 {
		t.Fatalf("summarize: %+v %v", res, err)
	}
	if rt.got[0].Locale != "en" || rt.got[0].Mode != "compact" {
		t.Fatalf("the runtime did not get the language of the alias: %+v", rt.got[0])
	}
	usage, _ := h.store.ListUsage(ctx, application.DefaultConfig().OrgID)
	found := false
	for _, u := range usage {
		if u.AgentID == "sales" && u.InputTokens == 900 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the summary must be in the usage ledger: %+v", usage)
	}

	// a runtime failure never breaks /compact: the local extractive summary answers
	rt.err = errors.New("boom")
	res, err = h.orch.Summarize(ctx, "", in)
	if err != nil || res.Source != "local" || !strings.HasPrefix(res.Text, "Conversation summary (2 messages)") {
		t.Fatalf("fallback: %+v %v", res, err)
	}
	in.Locale = "es"
	if res, _ = h.orch.Summarize(ctx, "", in); !strings.HasPrefix(res.Text, "Resumen de la conversación (2 mensajes)") {
		t.Fatalf("fallback in Spanish: %q", res.Text)
	}

	// no budget: refused before any model call
	h2 := newChat(t, &summaryRT{chatRT: newChatRuntime(fixedRoute("question", "general", primary("assistant"))), text: "x"}, func(c *application.Config) { c.BudgetUSD = 0.0000001 })
	_, err = h2.orch.Summarize(ctx, "sales", in)
	var ex *application.Exceeded
	if !errors.As(err, &ex) {
		t.Fatalf("want a budget error, got %v", err)
	}
}

func TestPostSystemNoticeCreatesTheConversation(t *testing.T) {
	h := newChat(t, newChatRuntime(fixedRoute("question", "general", primary("assistant"))), nil)
	if err := h.orch.PostSystemNotice(context.Background(), "agent:sales", "— nueva —"); err != nil {
		t.Fatal(err)
	}
	ms := h.messages("agent:sales")
	if len(ms) != 1 || ms[0].From != "system" || ms[0].Text != "— nueva —" {
		t.Fatalf("notice: %+v", ms)
	}
	if err := h.orch.PostSystemNotice(context.Background(), "agent:ghost", "x"); err == nil {
		t.Fatal("unknown agent must fail")
	}
	if err := h.orch.PostSystemNotice(context.Background(), "random", "x"); err == nil {
		t.Fatal("bad conversation must fail")
	}
}
