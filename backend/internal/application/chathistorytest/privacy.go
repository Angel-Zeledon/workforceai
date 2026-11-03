package chathistorytest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// E0 (docs/architecture/equipos-oficinas.md): Ana and Luis each have a private thread with the same agent;
// the office is shared; the old shared 1:1 history stays visible to both. One person never sees the other's
// thread in any view: the agent thread, General, the conversation list, the search, the unread counts.
func privacy(t *testing.T, f Fixture) {
	ctx := context.Background()
	const ana, luis = "u-ana", "u-luis"
	anaConv, luisConv := domain.ChatPrivateConv("legal", ana), domain.ChatPrivateConv("legal", luis)
	for _, id := range []string{"office", "agent:legal", anaConv, luisConv} {
		if err := f.Core.CreateConversation(ctx, f.Org, domain.Conversation{ID: id, Title: "t " + id, Participants: []string{"user"}, LastMessageAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	add := func(id, conv, from, to, turn, text, author string) {
		n++
		m := domain.Message{ID: id, ConversationID: conv, From: from, To: to, Kind: domain.MsgChat, Text: text,
			TS: time.Now().UTC().Add(time.Hour + time.Duration(n)*time.Second), TurnID: turn, AuthorID: author}
		if err := f.Core.AddMessage(ctx, f.Org, m); err != nil {
			t.Fatal(err)
		}
	}
	add("old", "agent:legal", "user", "legal", "T0", "historial viejo compartido", "")
	add("oldr", "agent:legal", "legal", "user", "T0", "respuesta vieja", "")
	add("a1", anaConv, "user", "legal", "TA", "contrato secreto de Ana", ana)
	add("a2", anaConv, "legal", "user", "TA", "respuesta privada para Ana", "")
	add("l1", luisConv, "user", "legal", "TL", "consulta de Luis", luis)
	add("l2", luisConv, "legal", "user", "TL", "respuesta privada para Luis", "")
	add("o1", "office", "user", "all", "TO", "hola equipo desde Ana", ana)
	add("o2", "office", "legal", "user", "TO", "hola a todos", "")

	view := func(viewer, key string) string {
		ms, _, err := f.Hist.ChatThreadMessages(ctx, f.Org, application.ChatThreadQuery{Viewer: viewer, Thread: key, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, m := range ms {
			b.WriteString(m.Text + "|")
		}
		return b.String()
	}
	for _, key := range []string{"legal", application.ThreadGeneral} {
		a, l := view(ana, key), view(luis, key)
		if !strings.Contains(a, "secreto") || strings.Contains(a, "Luis") || !strings.Contains(a, "privada para Ana") {
			t.Fatalf("ana's %s view = %s", key, a)
		}
		if !strings.Contains(l, "consulta de Luis") || strings.Contains(l, "privada para Ana") || strings.Contains(l, "secreto") {
			t.Fatalf("luis' %s view = %s", key, l)
		}
		for _, v := range []string{a, l} {
			if !strings.Contains(v, "historial viejo compartido") {
				t.Fatalf("the old shared history must stay visible in %s: %s", key, v)
			}
		}
		if !strings.Contains(l, "hola equipo desde Ana") {
			t.Fatalf("the office is shared: %s", l)
		}
	}
	// a third person (an administrator) sees neither private thread
	if adm := view("u-admin", "legal"); strings.Contains(adm, "secreto") || strings.Contains(adm, "Luis") {
		t.Fatalf("admin view = %s", adm)
	}
	// a cursor from another person's thread is not found
	if _, _, err := f.Hist.ChatThreadMessages(ctx, f.Org, application.ChatThreadQuery{Viewer: luis, Thread: "legal", BeforeID: "a2", Limit: 5}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cursor into ana's thread = %v", err)
	}

	// list and search
	rows, _, err := f.Hist.ChatConversations(ctx, f.Org, application.ChatConvQuery{Viewer: luis, Q: "secreto", Limit: 50})
	if err != nil || len(rows) != 0 {
		t.Fatalf("luis' search for ana's text = %+v, %v", rows, err)
	}
	rows, _, _ = f.Hist.ChatConversations(ctx, f.Org, application.ChatConvQuery{Viewer: luis, Limit: 50})
	for _, r := range rows {
		if r.ID == anaConv {
			t.Fatalf("luis lists ana's thread: %+v", r)
		}
	}
	rows, _, _ = f.Hist.ChatConversations(ctx, f.Org, application.ChatConvQuery{Viewer: ana, Q: "secreto", Limit: 50})
	if len(rows) != 1 || rows[0].ID != anaConv || rows[0].OwnerID != ana {
		t.Fatalf("ana's own search = %+v", rows)
	}
	rows, _, _ = f.Hist.ChatConversations(ctx, f.Org, application.ChatConvQuery{Viewer: luis, AgentID: "legal", Limit: 50})
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	if !got[luisConv] || !got["agent:legal"] || got[anaConv] {
		t.Fatalf("luis' legal conversations = %v", got)
	}
	if _, err := f.Hist.ChatGetConversation(ctx, f.Org, luis, anaConv); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("luis gets ana's conversation = %v", err)
	}

	// unread per person: what you wrote is never unread; Ana's office message is unread for Luis
	th := func(viewer, key string) application.ChatThread {
		ts, err := f.Hist.ChatThreads(ctx, f.Org, viewer, []string{"legal"})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range ts {
			if x.Key == key {
				return x
			}
		}
		t.Fatalf("no thread %s", key)
		return application.ChatThread{}
	}
	// visible to Luis and not his: oldr, l2, o1 (Ana's office message), o2; "old" has no author (before E0)
	if u := th(luis, application.ThreadGeneral).Unread; u != 4 {
		t.Fatalf("luis general unread = %d", u)
	}
	if err := f.Hist.ChatMarkRead(ctx, f.Org, luis, "thread:legal", time.Now().UTC().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if u := th(luis, "legal").Unread; u != 0 {
		t.Fatalf("luis read legal, unread = %d", u)
	}
	if u := th(ana, "legal").Unread; u == 0 {
		t.Fatal("luis' marker must not read ana's thread")
	}
	if c, _ := f.Hist.ChatGetConversation(ctx, f.Org, ana, anaConv); c.Unread == 0 {
		t.Fatal("ana's conversation unread must stay")
	}
	if c, _ := f.Hist.ChatGetConversation(ctx, f.Org, luis, luisConv); c.Unread != 0 {
		t.Fatalf("luis' conversation was read with the thread: %+v", c)
	}
}
