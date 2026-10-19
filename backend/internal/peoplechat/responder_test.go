package peoplechat

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeReplier struct {
	mu    sync.Mutex
	calls []string
	text  string
	hist  [][]HistoryItem
}

func (f *fakeReplier) Reply(_ context.Context, _, agent, text string, h []HistoryItem) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, agent+":"+text)
	f.hist = append(f.hist, h)
	return f.text, nil
}

func wireResponder(t *testing.T, rep Replier) (*Service, *MentionResponder, context.Context, string, *time.Time) {
	svc, _, ctx := setup(t)
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	r := &MentionResponder{Svc: svc, Replier: rep, Limit: 2, Window: time.Minute, Now: func() time.Time { return now }, Go: func(f func()) { f() }}
	svc.OnAgentMention = r.Handle
	g, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "deal", Members: []Member{{Kind: MemberAgent, ID: "sales"}, {Kind: MemberAgent, ID: "legal"}}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, r, ctx, g.ID, &now
}

func TestMentionGetsAReply(t *testing.T) {
	rep := &fakeReplier{text: "On it."}
	svc, _, ctx, ch, _ := wireResponder(t, rep)
	_, _ = svc.Post(ctx, "o1", ana, ch, "hola")
	_, _ = svc.Post(ctx, "o1", ana, ch, "@sofia draft the offer")
	if len(rep.calls) != 1 || rep.calls[0] != "sales:@sofia draft the offer" {
		t.Fatalf("calls = %v", rep.calls)
	}
	if len(rep.hist[0]) != 1 || rep.hist[0][0].Text != "hola" || rep.hist[0][0].From != "user" {
		t.Errorf("history = %+v", rep.hist[0])
	}
	msgs, _ := svc.Store.Messages(ctx, "o1", ch, 0, 10)
	last := msgs[len(msgs)-1]
	if last.AuthorKind != MemberAgent || last.AuthorID != "sales" || last.Body != "On it." {
		t.Errorf("last message = %+v", last)
	}
}

func TestAgentMessageNeverTriggersAnotherAgent(t *testing.T) {
	// the agent's reply mentions the other agent: it must not answer (no agent-to-agent loop)
	rep := &fakeReplier{text: "@leo please review"}
	svc, _, ctx, ch, _ := wireResponder(t, rep)
	_, _ = svc.Post(ctx, "o1", ana, ch, "@sofia hi")
	if len(rep.calls) != 1 {
		t.Fatalf("expected exactly one reply, got %v", rep.calls)
	}
}

func TestRateLimitPerChannel(t *testing.T) {
	rep := &fakeReplier{text: "ok"}
	svc, _, ctx, ch, now := wireResponder(t, rep)
	for i := 0; i < 5; i++ {
		_, _ = svc.Post(ctx, "o1", ana, ch, "@sofia ping")
	}
	if len(rep.calls) != 2 {
		t.Fatalf("limit 2 per window, got %d replies", len(rep.calls))
	}
	*now = now.Add(2 * time.Minute)
	_, _ = svc.Post(ctx, "o1", ana, ch, "@sofia ping")
	if len(rep.calls) != 3 {
		t.Errorf("the window should have reopened, got %d", len(rep.calls))
	}
}

func TestNoReplyWhenReplierHasNothingToSay(t *testing.T) {
	rep := &fakeReplier{text: ""}
	svc, _, ctx, ch, _ := wireResponder(t, rep)
	_, _ = svc.Post(ctx, "o1", ana, ch, "@sofia hi")
	msgs, _ := svc.Store.Messages(ctx, "o1", ch, 0, 10)
	if len(msgs) != 1 {
		t.Errorf("no reply expected, got %d messages", len(msgs))
	}
}

func TestInFlightGuard(t *testing.T) {
	r := &MentionResponder{}
	if !r.begin("k") || r.begin("k") {
		t.Fatal("second concurrent reply for the same agent+channel must be refused")
	}
	r.end("k")
	if !r.begin("k") {
		t.Fatal("after end it can start again")
	}
}
