package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// E0 migrations 807 (authors, private threads, per-person read markers) and 808 (person in the cost ledger):
// the columns round-trip, the ledger keeps the person, and down + up (twice) work on a database with data.
func TestMigration807And808PeopleColumns(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-p")
	priv := domain.ChatPrivateConv("legal", "u-ana")
	for _, id := range []string{"office", priv} {
		if err := st.CreateConversation(ctx, "org-p", domain.Conversation{ID: id, Title: id, LastMessageAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	m := domain.Message{ID: "m1", ConversationID: priv, From: "user", To: "legal", Kind: "chat", Text: "hola", TS: time.Now().UTC(),
		AuthorID: "u-ana", AuthorName: "Ana"}
	if err := st.AddMessage(ctx, "org-p", m); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListMessages(ctx, "org-p", priv)
	if err != nil || len(got) != 1 || got[0].AuthorID != "u-ana" || got[0].AuthorName != "Ana" {
		t.Fatalf("author round trip = %+v, %v", got, err)
	}
	owner := env.owner(t)
	var msgOwner, convOwner string
	_ = owner.QueryRow(ctx, `SELECT owner_id FROM messages WHERE id='m1'`).Scan(&msgOwner)
	_ = owner.QueryRow(ctx, `SELECT owner_id FROM conversations WHERE org_id='org-p' AND id=$1`, priv).Scan(&convOwner)
	if msgOwner != "u-ana" || convOwner != "u-ana" {
		t.Fatalf("owner columns = %q / %q", msgOwner, convOwner)
	}
	if err := st.AddUsage(ctx, "org-p", domain.UsageEntry{ID: "use1", AgentID: "legal", Kind: domain.UsageChat, CostUSD: 0.01, UserID: "u-ana"}); err != nil {
		t.Fatal(err)
	}
	us, err := st.ListUsage(ctx, "org-p")
	if err != nil || len(us) != 1 || us[0].UserID != "u-ana" {
		t.Fatalf("usage person = %+v, %v", us, err)
	}
	// two people may hold a marker for the same key (the primary key includes the person)
	for _, u := range []string{"u-ana", "u-luis", ""} {
		if err := st.ChatMarkRead(ctx, "org-p", u, "thread:general", time.Now().UTC()); err != nil {
			t.Fatalf("marker of %q: %v", u, err)
		}
	}

	for _, f := range []string{"808_usage_person_down.sql", "807_chat_people_down.sql"} {
		down, err := os.ReadFile("../../../migrations/down/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, string(down)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	var n int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()
		AND ((table_name = 'messages' AND column_name IN ('author_id','author_name','owner_id'))
		  OR (table_name = 'conversations' AND column_name = 'owner_id')
		  OR (table_name = 'chat_reads' AND column_name = 'user_id')
		  OR (table_name = 'usage_entries' AND column_name = 'user_id'))`).Scan(&n)
	if n != 0 {
		t.Fatalf("down left %d columns", n)
	}
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id='m1'`).Scan(&n)
	if n != 1 {
		t.Fatal("down must not delete messages")
	}
	for i := 0; i < 2; i++ { // idempotent
		for _, f := range []string{"807_chat_people.sql", "808_usage_person.sql"} {
			up, err := migrationsFS.ReadFile("migrations/" + f)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, string(up)); err != nil {
				t.Fatalf("%s again (%d): %v", f, i, err)
			}
		}
	}
	if err := st.ChatMarkRead(ctx, "org-p", "u-luis", "thread:general", time.Now().UTC()); err != nil {
		t.Fatalf("marker after up again: %v", err)
	}
}
