package peoplechat

import (
	"context"
	"sync"
	"time"
)

// HistoryItem is one earlier line of the channel handed to the agent as DATA. From is "user" or the agent id.
type HistoryItem struct{ From, Text string }

// Replier produces the short reply of an agent (the orchestrator implements it through the existing chat-reply
// runtime call: budget reservation, usage ledger, kill switch and a canned fallback in simulation).
type Replier interface {
	Reply(ctx context.Context, org, agentID, text string, history []HistoryItem) (string, error)
}

// MentionResponder answers @mentions of agents in people channels. Guard rails, all enforced here:
//   - only a PERSON's message triggers a reply (an agent's message never does: no agent-to-agent loops);
//   - at most Limit replies per channel in each Window (extra mentions are dropped silently);
//   - one reply at a time per (channel, agent);
//   - the reply runs outside the writer's request, with its own timeout, and posts through PostAsAgent, which
//     only lets a member agent speak.
type MentionResponder struct {
	Svc     *Service
	Replier Replier
	Limit   int           // replies per channel per window (default 4)
	Window  time.Duration // default 1 minute
	History int           // lines of context (default 8)
	Timeout time.Duration // default 60s
	Now     func() time.Time
	// Go runs the reply; nil starts a goroutine. Tests pass a synchronous runner.
	Go func(func())

	mu       sync.Mutex
	hits     map[string][]time.Time
	inFlight map[string]bool
}

func (r *MentionResponder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Allow records an attempt for the channel and says whether it is within the rate limit.
func (r *MentionResponder) allow(org, channel string) bool {
	limit, window := r.Limit, r.Window
	if limit <= 0 {
		limit = 4
	}
	if window <= 0 {
		window = time.Minute
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hits == nil {
		r.hits = map[string][]time.Time{}
	}
	key := org + "/" + channel
	kept := r.hits[key][:0]
	for _, t := range r.hits[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		r.hits[key] = kept
		return false
	}
	r.hits[key] = append(kept, now)
	return true
}

func (r *MentionResponder) begin(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight == nil {
		r.inFlight = map[string]bool{}
	}
	if r.inFlight[key] {
		return false
	}
	r.inFlight[key] = true
	return true
}

func (r *MentionResponder) end(key string) {
	r.mu.Lock()
	delete(r.inFlight, key)
	r.mu.Unlock()
}

// Handle is Service.OnAgentMention. It never blocks the writer.
func (r *MentionResponder) Handle(_ context.Context, org string, ch Channel, m Message, agentID string) {
	if r.Svc == nil || r.Replier == nil || m.AuthorKind != MemberUser || ch.Archived {
		return
	}
	key := org + "/" + ch.ID + "/" + agentID
	if !r.begin(key) {
		return
	}
	if !r.allow(org, ch.ID) {
		r.end(key)
		return
	}
	run := r.Go
	if run == nil {
		run = func(f func()) { go f() }
	}
	run(func() {
		defer r.end(key)
		timeout := r.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		n := r.History
		if n <= 0 {
			n = 8
		}
		var hist []HistoryItem
		if prev, err := r.Svc.Store.Messages(ctx, org, ch.ID, m.Seq, n); err == nil {
			for _, p := range prev {
				from := "user"
				if p.AuthorKind == MemberAgent {
					from = p.AuthorID
				}
				hist = append(hist, HistoryItem{From: from, Text: p.Body})
			}
		}
		text, err := r.Replier.Reply(ctx, org, agentID, m.Body, hist)
		if err != nil || text == "" {
			return
		}
		_, _ = r.Svc.PostAsAgent(ctx, org, agentID, ch.ID, text)
	})
}
