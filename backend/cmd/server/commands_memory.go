package main

import (
	"context"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/memory"
)

// commandsMemory backs /memoria and /recordar with the long-term memory
// (internal/memory): what the agent recalls, the facts a person tells it to
// keep, and forgetting (audited, like the Memory tab).
type commandsMemory struct{ svc *memory.Service }

var _ api.LongMemory = commandsMemory{}

func (m commandsMemory) List(ctx context.Context, org, agentID string) ([]api.MemoryItem, error) {
	items, err := m.svc.List(ctx, org, agentID, 50)
	if err != nil {
		return nil, err
	}
	out := make([]api.MemoryItem, 0, len(items))
	for _, it := range items {
		v := it.Title
		if it.Snippet != "" && it.Kind != memory.KindNote {
			v += ": " + it.Snippet
		}
		out = append(out, api.MemoryItem{Scope: it.Kind, Key: it.Ref, Value: v})
	}
	return out, nil
}

func (m commandsMemory) Remember(ctx context.Context, org, agentID, text string) error {
	return m.svc.RememberNote(ctx, org, agentID, text)
}

// Forget deletes what the agent remembers about `text`: the best matches of a
// search over its memory (documents only; files are forgotten in the drive).
func (m commandsMemory) Forget(ctx context.Context, org, agentID, text string) (int, error) {
	items, err := m.svc.Search(ctx, org, agentID, text, 10)
	if err != nil {
		return 0, err
	}
	actor := application.ActorFrom(ctx, "user")
	n := 0
	for _, it := range items {
		if it.ID == "" {
			continue
		}
		if err := m.svc.Delete(ctx, org, agentID, it.ID, actor); err == nil {
			n++
		}
	}
	return n, nil
}

// longMemoryFor is nil when the memory service is not configured, so /memoria
// falls back to the agent memory store.
func longMemoryFor(svc *memory.Service) api.LongMemory {
	if svc == nil {
		return nil
	}
	return commandsMemory{svc: svc}
}
