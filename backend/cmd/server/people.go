package main

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/peoplechat"
	"aiworkforce/backend/internal/presence"
)

// wirePeople builds the human presence hub and the people chat (docs/architecture/presence.md). Chat is stored in
// Postgres when there is a database and in memory otherwise (demo). The presence state itself is in memory.
func wirePeople(ctx context.Context, pg *postgres.Store, pub events.Publisher, queries *application.Queries) (*presence.Hub, *peoplechat.Service) {
	ph := presence.New(pub, presence.Config{})
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
	return ph, chat
}
