package chathistorytest

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// officeFilter: the store leaves out, in the query, the messages of the offices the context hides and keeps to the one
// asked (memory and Postgres answer the same): threads, previews, counts, unread, the conversation list, its search,
// and a page is full of what may be read ("has more" is true to it).
func officeFilter(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, id := range []string{"office", "agent:legal"} {
		if err := f.Core.CreateConversation(ctx, f.Org, domain.Conversation{ID: id, Title: "", Participants: []string{"user"}, LastMessageAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	add := func(conv, from, to, text, office string) {
		n++
		m := domain.Message{ID: "m" + string(rune('a'+n)), ConversationID: conv, From: from, To: to, Kind: domain.MsgChat, Text: text,
			TS: time.Now().UTC().Add(time.Hour + time.Duration(n)*time.Second), TurnID: "T" + string(rune('a'+n)), OfficeID: office}
		if err := f.Core.AddMessage(ctx, f.Org, m); err != nil {
			t.Fatal(err)
		}
	}
	add("office", "user", "all", "general uno", "")
	add("office", "user", "all", "secreto uno", "priv")
	add("office", "user", "all", "secreto dos", "priv")
	add("agent:legal", "user", "legal", "consulta secreta", "priv")
	add("office", "user", "all", "general dos", "")
	add("office", "user", "all", "ventas uno", "ventas")
	add("office", "user", "all", "general tres", "general") // General written with its own name

	hide := application.WithMessageOffices(ctx, application.MessageOffices{Hide: []string{"priv"}})
	asked := application.WithMessageOffices(ctx, application.MessageOffices{}.WithOfficeAsked("ventas"))
	general := application.WithMessageOffices(ctx, application.MessageOffices{}.WithOfficeAsked("general"))
	texts := func(c context.Context, limit int, before string) (string, bool) {
		ms, more, err := f.Hist.ChatThreadMessages(c, f.Org, application.ChatThreadQuery{Thread: application.ThreadGeneral, Limit: limit, BeforeID: before})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, m := range ms {
			b.WriteString(m.Text + "|")
		}
		return b.String(), more
	}
	if got, _ := texts(hide, 100, ""); strings.Contains(got, "secret") || !strings.Contains(got, "general uno") || !strings.Contains(got, "ventas uno") {
		t.Fatalf("hidden office: %s", got)
	}
	// a page of 2 is the last two the person may read, and has more: the hidden rows between do not make a short page
	if got, more := texts(hide, 2, ""); got != "ventas uno|general tres|" || !more {
		t.Fatalf("page of 2 = %s (more %v)", got, more)
	}
	if got, more := texts(hide, 2, "mh"); got != "general dos|ventas uno|" || !more {
		t.Fatalf("second page = %s (more %v)", got, more)
	}
	if got, more := texts(hide, 2, "mf"); got != "general uno|" || more {
		t.Fatalf("third page = %s (more %v)", got, more)
	}
	if got, more := texts(asked, 100, ""); got != "ventas uno|" || more {
		t.Fatalf("office asked = %s (more %v)", got, more)
	}
	if got, _ := texts(general, 100, ""); got != "general uno|general dos|general tres|" {
		t.Fatalf("General asked = %s", got)
	}
	// the thread of the agent: only what was said in the hidden office, so it has nothing for this person
	ms, _, err := f.Hist.ChatThreadMessages(hide, f.Org, application.ChatThreadQuery{Thread: "legal", Limit: 100})
	if err != nil || len(ms) != 0 {
		t.Fatalf("agent thread = %v (%v)", ms, err)
	}

	threads, err := f.Hist.ChatThreads(hide, f.Org, "", []string{"legal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g := threads[0]; g.MessageCount != 4 || strings.Contains(g.Preview, "secret") || g.Preview != "general tres" {
		t.Fatalf("General thread = %+v", g)
	}
	if l := threads[1]; l.MessageCount != 0 || l.Preview != "" {
		t.Fatalf("agent thread entry = %+v", l)
	}

	rows, _, err := f.Hist.ChatConversations(hide, f.Org, application.ChatConvQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]application.ChatConversation{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if _, ok := byID["agent:legal"]; ok {
		t.Fatalf("a conversation said only in the hidden office is listed: %+v", rows)
	}
	if o := byID["office"]; o.MessageCount != 4 || o.Preview != "general tres" {
		t.Fatalf("office row = %+v", o)
	}
	rows, _, err = f.Hist.ChatConversations(hide, f.Org, application.ChatConvQuery{Q: "secret", Limit: 100})
	if err != nil || len(rows) != 0 {
		t.Fatalf("search finds the hidden office: %+v (%v)", rows, err)
	}
	rows, _, err = f.Hist.ChatConversations(ctx, f.Org, application.ChatConvQuery{Q: "secret", Limit: 100})
	if err != nil || len(rows) != 2 {
		t.Fatalf("search without filter = %+v (%v)", rows, err)
	}
}
