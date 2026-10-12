package memory_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application/chathistorytest"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// The in-memory store passes the same chat history suite as Postgres.
func TestChatHistorySuite(t *testing.T) {
	chathistorytest.Run(t, func(t *testing.T) chathistorytest.Fixture {
		st := memory.New()
		if err := st.Seed(context.Background(), domain.SeedOrg(10), roles.SeedAgents()); err != nil {
			t.Fatal(err)
		}
		return chathistorytest.Fixture{Org: "org-a", Core: st, Hist: st}
	})
}
