package chathistorytest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func paging(t *testing.T, f Fixture) {
	s := seed(t, f)
	p1, more := thread(t, s, application.ThreadGeneral, "", 4)
	if !more {
		t.Fatal("more expected")
	}
	eq(t, "page 1 (newest four, oldest first)", labels(s, p1), []string{"s3", "u4", "a4", "s4"})
	p2, more := thread(t, s, application.ThreadGeneral, p1[0].ID, 4)
	if !more {
		t.Fatal("more expected on page 2")
	}
	eq(t, "page 2", labels(s, p2), []string{"c1", "u2", "c2", "u3"})
	p3, more := thread(t, s, application.ThreadGeneral, p2[0].ID, 4)
	if more {
		t.Fatal("page 3 is the last")
	}
	eq(t, "page 3", labels(s, p3), []string{"u1", "a1"})
	// an agent view pages too
	a1, more := thread(t, s, "accounting", "", 3)
	if !more {
		t.Fatal("accounting more expected")
	}
	eq(t, "accounting newest 3", labels(s, a1), []string{"c1", "u2", "c2"})
	// a cursor that does not exist is a not-found, not an empty page
	_, _, err := f.Hist.ChatThreadMessages(context.Background(), f.Org, application.ChatThreadQuery{Thread: "sales", BeforeID: "nope", Limit: 5})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad cursor = %v", err)
	}
}

func ids(rows []application.ChatConversation) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func list(t *testing.T, s *seeded, q application.ChatConvQuery) ([]application.ChatConversation, bool) {
	t.Helper()
	rows, more, err := s.f.Hist.ChatConversations(context.Background(), s.f.Org, q)
	if err != nil {
		t.Fatalf("list %+v: %v", q, err)
	}
	return rows, more
}

func listAndSearch(t *testing.T, f Fixture) {
	s := seed(t, f)
	rows, _ := list(t, s, application.ChatConvQuery{})
	// newest activity first
	eq(t, "all", ids(rows), []string{"rq1", "office", "agent:sales", "agent:accounting"})
	by := map[string]application.ChatConversation{}
	for _, r := range rows {
		by[r.ID] = r
	}
	if o := by["office"]; o.Kind != "office" || o.MessageCount != 8 || o.Preview != "Me encargo del borrador" || o.PreviewFrom != "sales" || o.Title != "Oficina" {
		t.Fatalf("office row = %+v", o)
	}
	if a := by["agent:sales"]; a.Kind != "agent" || a.AgentID != "sales" || a.MessageCount != 2 || !slices.Contains(a.Participants, "sales") {
		t.Fatalf("agent row = %+v", a)
	}
	if r := by["rq1"]; r.Kind != "request" || r.RequestID == nil || *r.RequestID != "req-1" || r.Title != "Preparar la propuesta" {
		t.Fatalf("request row = %+v", r)
	}
	if e := by["agent:accounting"]; e.MessageCount != 0 || e.Preview != "" {
		t.Fatalf("empty chat row = %+v", e)
	}
	if !rows[0].UpdatedAt.Equal(rows[0].LastMessageAt) || rows[0].UpdatedAt.IsZero() {
		t.Fatalf("updated_at/last_message_at = %v %v", rows[0].UpdatedAt, rows[0].LastMessageAt)
	}

	// per agent: its 1:1 chat plus the conversations where it took part
	rows, _ = list(t, s, application.ChatConvQuery{AgentID: "sales"})
	eq(t, "agent sales", ids(rows), []string{"rq1", "office", "agent:sales"})
	rows, _ = list(t, s, application.ChatConvQuery{AgentID: "accounting"})
	eq(t, "agent accounting", ids(rows), []string{"office", "agent:accounting"})
	rows, _ = list(t, s, application.ChatConvQuery{AgentID: "hr"})
	if len(rows) != 0 {
		t.Fatalf("hr = %v", ids(rows))
	}
	rows, _ = list(t, s, application.ChatConvQuery{Kind: "request"})
	eq(t, "kind request", ids(rows), []string{"rq1"})
	rows, _ = list(t, s, application.ChatConvQuery{Kind: "agent"})
	eq(t, "kind agent", ids(rows), []string{"agent:sales", "agent:accounting"})

	// search in the content (case-insensitive) and in the title
	rows, _ = list(t, s, application.ChatConvQuery{Q: "PIPELINE"})
	eq(t, "search content", ids(rows), []string{"agent:sales"})
	rows, _ = list(t, s, application.ChatConvQuery{Q: "preparar la prop"})
	eq(t, "search title", ids(rows), []string{"rq1"})
	rows, _ = list(t, s, application.ChatConvQuery{Q: "margen", AgentID: "accounting"})
	eq(t, "search + agent", ids(rows), []string{"office"})
	// wildcard characters are literal
	rows, _ = list(t, s, application.ChatConvQuery{Q: "%"})
	eq(t, "search percent", ids(rows), []string{"agent:sales"})
	rows, _ = list(t, s, application.ChatConvQuery{Q: "_"})
	if len(rows) != 0 {
		t.Fatalf("search underscore = %v", ids(rows))
	}
	rows, _ = list(t, s, application.ChatConvQuery{Q: "xyz-no-match"})
	if len(rows) != 0 {
		t.Fatalf("no match = %v", ids(rows))
	}

	// paging
	rows, more := list(t, s, application.ChatConvQuery{Limit: 2})
	eq(t, "page 1", ids(rows), []string{"rq1", "office"})
	if !more {
		t.Fatal("more expected")
	}
	rows, more = list(t, s, application.ChatConvQuery{Limit: 2, Offset: 2})
	eq(t, "page 2", ids(rows), []string{"agent:sales", "agent:accounting"})
	if more {
		t.Fatal("last page")
	}
	rows, _ = list(t, s, application.ChatConvQuery{Limit: 2, Offset: 9})
	if len(rows) != 0 {
		t.Fatalf("past the end = %v", ids(rows))
	}
}

func renameArchive(t *testing.T, f Fixture) {
	s := seed(t, f)
	ctx := context.Background()
	h := f.Hist
	title := "Propuesta Acme"
	if err := h.ChatPatchConversation(ctx, f.Org, "rq1", application.ChatConvPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	c, err := h.ChatGetConversation(ctx, f.Org, "rq1")
	if err != nil || c.Title != title || !c.TitleCustom || c.Archived {
		t.Fatalf("renamed = %+v, %v", c, err)
	}
	rows, _ := list(t, s, application.ChatConvQuery{Q: "acme"})
	eq(t, "search new title", ids(rows), []string{"rq1"})

	yes, no := true, false
	if err := h.ChatPatchConversation(ctx, f.Org, "rq1", application.ChatConvPatch{Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	c, _ = h.ChatGetConversation(ctx, f.Org, "rq1")
	if !c.Archived || c.ArchivedAt == nil || c.Title != title {
		t.Fatalf("archived = %+v", c)
	}
	rows, _ = list(t, s, application.ChatConvQuery{})
	eq(t, "hidden by default", ids(rows), []string{"office", "agent:sales", "agent:accounting"})
	rows, _ = list(t, s, application.ChatConvQuery{Archived: "only"})
	eq(t, "archived only", ids(rows), []string{"rq1"})
	rows, _ = list(t, s, application.ChatConvQuery{Archived: "all"})
	if len(rows) != 4 {
		t.Fatalf("archived all = %v", ids(rows))
	}
	// archiving twice keeps the first timestamp; unarchive restores it
	if err := h.ChatPatchConversation(ctx, f.Org, "rq1", application.ChatConvPatch{Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	c2, _ := h.ChatGetConversation(ctx, f.Org, "rq1")
	if c2.ArchivedAt == nil || !c2.ArchivedAt.Equal(*c.ArchivedAt) {
		t.Fatalf("archived_at changed: %v vs %v", c2.ArchivedAt, c.ArchivedAt)
	}
	if err := h.ChatPatchConversation(ctx, f.Org, "rq1", application.ChatConvPatch{Archived: &no}); err != nil {
		t.Fatal(err)
	}
	rows, _ = list(t, s, application.ChatConvQuery{})
	if len(rows) != 4 {
		t.Fatalf("after unarchive = %v", ids(rows))
	}
	if err := h.ChatPatchConversation(ctx, f.Org, "ghost", application.ChatConvPatch{Archived: &yes}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ghost = %v", err)
	}
}

func threadOf(t *testing.T, s *seeded, key string) application.ChatThread {
	t.Helper()
	ts, err := s.f.Hist.ChatThreads(context.Background(), s.f.Org, []string{"sales", "accounting", "hr"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 4 || ts[0].Key != application.ThreadGeneral {
		t.Fatalf("threads = %+v", ts)
	}
	for _, x := range ts {
		if x.Key == key {
			return x
		}
	}
	t.Fatalf("thread %s missing", key)
	return application.ChatThread{}
}

func unread(t *testing.T, f Fixture) {
	s := seed(t, f)
	ctx := context.Background()
	h := f.Hist
	g := threadOf(t, s, "general")
	// non-user messages of the chat streams: a1 c1 c2 s3 a4 s4
	if g.MessageCount != 10 || g.Unread != 6 || g.Preview != "Me encargo del borrador" || g.PreviewFrom != "sales" {
		t.Fatalf("general = %+v", g)
	}
	if a := threadOf(t, s, "accounting"); a.MessageCount != 4 || a.Unread != 2 || a.AgentID != "accounting" {
		t.Fatalf("accounting = %+v", a)
	}
	if hr := threadOf(t, s, "hr"); hr.MessageCount != 0 || hr.Unread != 0 || hr.Preview != "" {
		t.Fatalf("hr = %+v", hr)
	}
	rows, _ := list(t, s, application.ChatConvQuery{})
	un := map[string]int{}
	for _, r := range rows {
		un[r.ID] = r.Unread
	}
	if un["office"] != 5 || un["agent:sales"] != 1 || un["rq1"] != 1 || un["agent:accounting"] != 0 {
		t.Fatalf("conversation unread = %v", un)
	}

	// reading General reads every chat stream, but not the agents' own markers or request conversations
	if err := h.ChatMarkRead(ctx, f.Org, "thread:general", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if g := threadOf(t, s, "general"); g.Unread != 0 {
		t.Fatalf("general after read = %+v", g)
	}
	rows, _ = list(t, s, application.ChatConvQuery{})
	for _, r := range rows {
		want := 0
		if r.ID == "rq1" {
			want = 1
		}
		if r.Unread != want {
			t.Fatalf("%s unread = %d, want %d", r.ID, r.Unread, want)
		}
	}
	if a := threadOf(t, s, "accounting"); a.Unread != 2 {
		t.Fatalf("accounting keeps its own marker: %+v", a)
	}
	if err := h.ChatMarkRead(ctx, f.Org, "thread:accounting", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if a := threadOf(t, s, "accounting"); a.Unread != 0 {
		t.Fatalf("accounting after read = %+v", a)
	}
	// a read marker never goes back in time
	if err := h.ChatMarkRead(ctx, f.Org, "thread:accounting", base); err != nil {
		t.Fatal(err)
	}
	if a := threadOf(t, s, "accounting"); a.Unread != 0 {
		t.Fatalf("marker moved back: %+v", a)
	}
	// a new agent message after the marker is unread; the user's own never is
	s.future = true
	s.msg(t, "c9", "office", "accounting", "user", "T9", "Una cosa más", "")
	s.msg(t, "u9", "office", "user", "all", "T10", "gracias", "")
	if g := threadOf(t, s, "general"); g.Unread != 1 {
		t.Fatalf("general after new message = %+v", g)
	}
	if a := threadOf(t, s, "accounting"); a.Unread != 1 {
		t.Fatalf("accounting after new message = %+v", a)
	}
	if err := h.ChatMarkRead(ctx, f.Org, "conv:rq1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.ChatGetConversation(ctx, f.Org, "rq1"); c.Unread != 0 {
		t.Fatalf("rq1 unread = %d", c.Unread)
	}
}

func del(t *testing.T, f Fixture) {
	s := seed(t, f)
	ctx := context.Background()
	h := f.Hist
	n, err := h.ChatDeleteConversation(ctx, f.Org, "agent:sales")
	if err != nil || n != 2 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	if _, err := h.ChatGetConversation(ctx, f.Org, "agent:sales"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	gen, _ := thread(t, s, application.ThreadGeneral, "", 100)
	eq(t, "general after delete", labels(s, gen), []string{"u1", "a1", "c1", "u2", "c2", "u4", "a4", "s4"})
	sales, _ := thread(t, s, "sales", "", 100)
	eq(t, "sales after delete", labels(s, sales), []string{"u4", "s4"})
	if _, err := h.ChatDeleteConversation(ctx, f.Org, "agent:sales"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	// a request conversation goes without touching the chat streams
	if n, err := h.ChatDeleteConversation(ctx, f.Org, "rq1"); err != nil || n != 1 {
		t.Fatalf("delete rq1 = %d, %v", n, err)
	}
	gen, _ = thread(t, s, application.ThreadGeneral, "", 100)
	if len(gen) != 8 {
		t.Fatalf("general = %v", labels(s, gen))
	}
}

func autoTitle(t *testing.T, f Fixture) {
	ctx := context.Background()
	if err := f.Core.CreateConversation(ctx, f.Org, domain.Conversation{ID: "tmp", Title: "", Participants: []string{"user"}, LastMessageAt: base}); err != nil {
		t.Fatal(err)
	}
	s := &seeded{f: f, ids: map[string]string{}}
	s.msg(t, "x1", "tmp", "assistant", "user", "", "Respuesta primero", "")
	s.msg(t, "x2", "tmp", "user", "assistant", "", "  Necesito   un informe\nde ventas ", "")
	c, err := f.Hist.ChatGetConversation(ctx, f.Org, "tmp")
	if err != nil || c.Title != "Necesito un informe de ventas" || c.TitleCustom {
		t.Fatalf("auto title = %+v, %v", c, err)
	}
}

func isolation(t *testing.T, f Fixture) {
	s := seed(t, f)
	ctx := context.Background()
	rows, _, err := f.Hist.ChatConversations(ctx, f.Other, application.ChatConvQuery{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("other org conversations = %v, %v", ids(rows), err)
	}
	ms, _, err := f.Hist.ChatThreadMessages(ctx, f.Other, application.ChatThreadQuery{Thread: application.ThreadGeneral, Limit: 50})
	if err != nil || len(ms) != 0 {
		t.Fatalf("other org thread = %d, %v", len(ms), err)
	}
	ts, err := f.Hist.ChatThreads(ctx, f.Other, []string{"sales"})
	if err != nil || ts[0].MessageCount != 0 || ts[1].MessageCount != 0 {
		t.Fatalf("other org threads = %+v, %v", ts, err)
	}
	if _, err := f.Hist.ChatGetConversation(ctx, f.Other, "office"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other org get = %v", err)
	}
	if err := f.Hist.ChatPatchConversation(ctx, f.Other, "office", application.ChatConvPatch{Title: ptr("hack")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other org patch = %v", err)
	}
	if _, err := f.Hist.ChatDeleteConversation(ctx, f.Other, "office"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other org delete = %v", err)
	}
	if gen, _ := thread(t, s, application.ThreadGeneral, "", 100); len(gen) != 10 {
		t.Fatalf("owner general = %d", len(gen))
	}
}
