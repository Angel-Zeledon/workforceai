package main

import (
	"context"
	"errors"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/meetings"
	"aiworkforce/backend/internal/peoplechat"
	"aiworkforce/backend/internal/presence"
)

// wirePeople builds the human presence hub and the people chat (docs/architecture/presence.md). Chat is stored in
// Postgres when there is a database and in memory otherwise (demo). The presence state itself is in memory.
func wirePeople(ctx context.Context, pg *postgres.Store, pub events.Publisher, queries *application.Queries, orch *application.Orchestrator) (*presence.Hub, *peoplechat.Service) {
	cfg := presence.Config{LeaveGrace: envDuration("PRESENCE_LEAVE_GRACE", 0)}
	if presenceScripter != nil { // several replicas share who is online through Redis; without it each replica is on its own
		cfg.Registry = &presence.RedisRegistry{R: presenceScripter}
	}
	ph := presence.New(pub, cfg)
	go ph.Run(ctx)
	var store peoplechat.Store = peoplechat.NewMemoryStore()
	if pg != nil {
		store = &peoplechat.PgStore{Pool: pg.Pool()}
	}
	chat := &peoplechat.Service{Store: store, Pub: pub,
		Agents: func(ctx context.Context, org string) ([]peoplechat.AgentRef, error) {
			list, err := queries.Agents(application.WithOrg(ctx, org))
			if err != nil {
				return nil, err
			}
			out := make([]peoplechat.AgentRef, 0, len(list))
			for _, a := range list {
				out = append(out, peoplechat.AgentRef{ID: a.ID, Name: a.Name})
			}
			return out, nil
		}}
	// An @mentioned agent that is a member of the channel answers with one short, cost-capped reply through the
	// orchestrator (guards, budget, ledger, canned fallback in simulation); it is rate limited per channel and an
	// agent's message never triggers another agent.
	chat.OnAgentMention = (&peoplechat.MentionResponder{Svc: chat, Replier: mentionReplier{orch: orch}}).Handle
	return ph, chat
}

// presenceScripter is set when Redis is available.
var presenceScripter goredis.Scripter

// envDuration reads a Go duration ("8s"; "-1s" = no grace) from the environment; unset or invalid gives def.
func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// meetingPeople gives the Meetings attendee picker the people directory of the people chat.
type meetingPeople struct{ dir peoplechat.Store }

func (m meetingPeople) People(ctx context.Context, org string) ([]meetings.PersonInfo, error) {
	list, err := m.dir.Directory(ctx, org)
	if err != nil {
		return nil, err
	}
	out := make([]meetings.PersonInfo, 0, len(list))
	for _, p := range list {
		out = append(out, meetings.PersonInfo{ID: p.ID, Name: p.Name, Role: p.Role})
	}
	return out, nil
}

type mentionReplier struct{ orch *application.Orchestrator }

func (m mentionReplier) Reply(ctx context.Context, org, agentID, text string, history []peoplechat.HistoryItem) (string, error) {
	items := make([]application.ChatHistoryItem, 0, len(history))
	for _, h := range history {
		items = append(items, application.ChatHistoryItem{From: h.From, Text: h.Text})
	}
	out, err := m.orch.PeopleChatReply(application.WithOrg(ctx, org), agentID, text, items)
	if errors.Is(err, application.ErrNoReply) {
		return "", nil
	}
	return out, err
}
