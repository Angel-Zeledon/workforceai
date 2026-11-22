package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/agenthistory"
	"aiworkforce/backend/internal/domain"
)

// Agent configuration history (migration 827): round trip, version numbering, retention, tenant isolation under RLS
// and the append-only trigger.
func TestAgentHistoryStorePostgres(t *testing.T) {
	st := testStore(t).Store
	hs := &AgentHistoryStore{S: st}
	ctx := context.Background()
	seedOrg(t, st, "org-h1")
	seedOrg(t, st, "org-h2")
	at := time.Now().UTC().Truncate(time.Microsecond)

	if _, had, err := hs.Last(ctx, "org-h1", "sales"); err != nil || had {
		t.Fatalf("empty last = %v %v", had, err)
	}
	for i := 1; i <= 5; i++ {
		v := agenthistory.Version{AgentID: "sales", Version: i, CreatedAt: at.Add(time.Duration(i) * time.Second), Actor: "u-ana", ActorKind: "person",
			ActorName: "Ana", Source: "PUT /agents/{id}/tone", Fields: []string{"tone"}, Config: agenthistory.Config{Name: "Valeria", Tone: "formal"}}
		if err := hs.Append(ctx, "org-h1", v, 3); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := hs.Append(ctx, "org-h1", agenthistory.Version{AgentID: "sales", Version: 5, Config: agenthistory.Config{}}, 3); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate version = %v", err)
	}
	l, err := hs.List(ctx, "org-h1", "sales", 0)
	if err != nil || len(l) != 3 || l[0].Version != 5 || l[2].Version != 3 || l[0].Config.Tone != "formal" || l[0].ActorName != "Ana" {
		t.Fatalf("list = %+v %v", l, err)
	}
	if _, err := hs.Get(ctx, "org-h1", "sales", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("version beyond the retention = %v", err)
	}
	if v, err := hs.Get(ctx, "org-h1", "sales", 4); err != nil || v.Fields[0] != "tone" {
		t.Fatalf("get = %+v %v", v, err)
	}
	if _, err := hs.Get(ctx, "org-h2", "sales", 4); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another org reads it: %v", err)
	}
	if l, _ := hs.List(ctx, "org-h2", "sales", 0); len(l) != 0 {
		t.Fatalf("org-h2 lists %+v", l)
	}
	err = st.WithOrgTx(ctx, "org-h1", func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE agent_config_versions SET source='x' WHERE org_id='org-h1'`)
		return e
	})
	if err == nil {
		t.Fatal("an update of the history was accepted")
	}
}
