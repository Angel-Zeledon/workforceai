package artifacts_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
)

type capture struct {
	mu     sync.Mutex
	events []domain.Event
}

func (c *capture) Publish(_ context.Context, e domain.Event) error {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	return nil
}

func (c *capture) count(typ string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func (c *capture) last(typ string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.events) - 1; i >= 0; i-- {
		if c.events[i].Type == typ {
			b, _ := json.Marshal(c.events[i].Payload)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			return m
		}
	}
	return nil
}

type asker struct{ texts []string }

func (a *asker) Submit(_ context.Context, text string) (string, error) {
	a.texts = append(a.texts, text)
	return "req-1", nil
}

type env struct {
	t     *testing.T
	svc   *artifacts.Service
	pub   *capture
	store *memory.Store
	ask   *asker
	ctx   context.Context
	me    artifacts.Actor
	admin artifacts.Actor
}

func newEnv(t *testing.T, mutate func(*artifacts.Config)) *env {
	t.Helper()
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(50), domain.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pub := &capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: store, Pub: pub, Log: log}
	ask := &asker{}
	cfg := artifacts.Config{Store: artifacts.NewMemStore(), Rec: rec, Core: store, Asker: ask, OrgID: domain.DemoOrgID, Log: log}
	if mutate != nil {
		mutate(&cfg)
	}
	return &env{t: t, svc: artifacts.New(cfg), pub: pub, store: store, ask: ask, ctx: context.Background(),
		me:    artifacts.Actor{Kind: artifacts.ActorUser, ID: "u1"},
		admin: artifacts.Actor{Kind: artifacts.ActorUser, ID: "boss", CanApprove: true, CanDelete: true}}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func sheet(cells string) string {
	return `{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Margen","rows":40,"cols":8,"cells":` + cells + `}]}`
}

func (e *env) create(a artifacts.Actor, in artifacts.CreateInput) artifacts.Artifact {
	e.t.Helper()
	art, err := e.svc.Create(e.ctx, a, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return art
}

func (e *env) sheetArt(cells string) artifacts.Artifact {
	return e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindSheet, Title: "Margen", Content: raw(sheet(cells))})
}

func cellV(t *testing.T, a artifacts.Artifact, addr string) float64 {
	t.Helper()
	var root struct {
		Sheets []struct {
			Cells map[string]struct {
				V any `json:"v"`
			} `json:"cells"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(a.Content, &root); err != nil {
		t.Fatal(err)
	}
	f, _ := root.Sheets[0].Cells[addr].V.(float64)
	return f
}

// ---- catalog and creation ----

func TestCreateEveryKindFromBlankAndTemplates(t *testing.T) {
	e := newEnv(t, nil)
	for _, k := range artifacts.Kinds {
		a := e.create(e.me, artifacts.CreateInput{Kind: k, Title: "x " + string(k)})
		if a.Kind != k || a.SchemaVersion != "aiw."+string(k)+"/1" || a.HeadVersion != 1 || a.Status != "draft" || a.Locked || a.BuildState != "done" {
			t.Fatalf("%s: %+v", k, a.Meta)
		}
	}
	for _, tp := range e.svc.Templates("", "") {
		a := e.create(e.me, artifacts.CreateInput{TemplateID: tp.ID, Title: tp.Title["en"], Locale: "en"})
		if a.Kind != tp.Kind {
			t.Fatalf("template %s produced %s", tp.ID, a.Kind)
		}
	}
	if got := e.svc.Templates("legal", ""); got[0].SuggestedFor[0] != "legal" {
		t.Fatalf("suggested templates must come first: %+v", got[0])
	}
	if ks := e.svc.Kinds("accounting"); ks[0].Kind != artifacts.KindSheet || !ks[0].Suggested || len(ks) != 9 {
		t.Fatalf("kinds: %+v", ks)
	}
	if n := len(must(e.svc.List(e.ctx, artifacts.Filter{}))); n < 9 {
		t.Fatalf("list: %d", n)
	}
	if n := len(must(e.svc.List(e.ctx, artifacts.Filter{Kind: "sheet"}))); n == 0 || n >= 9+14 {
		t.Fatalf("kind filter: %d", n)
	}
	if e.pub.count("artifact.created") < 9 {
		t.Fatal("artifact.created events missing")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestContentIsValidatedBeforeItIsStored(t *testing.T) {
	e := newEnv(t, nil)
	bad := map[string]artifacts.CreateInput{
		"wrong schema":      {Kind: "doc", Content: raw(`{"schema":"aiw.sheet/1"}`)},
		"hyperlink formula": {Kind: "sheet", Content: raw(sheet(`{"A1":{"f":"=HYPERLINK(\"http://evil\",\"x\")"}}`))},
		"indirect":          {Kind: "sheet", Content: raw(sheet(`{"A1":{"f":"=INDIRECT(\"B2\")"}}`))},
		"webservice":        {Kind: "sheet", Content: raw(sheet(`{"A1":{"f":"=WEBSERVICE(\"http://x\")"}}`))},
		"external ref":      {Kind: "sheet", Content: raw(sheet(`{"A1":{"f":"=SUM([book.xlsx]S1!A1)"}}`))},
		"bad address":       {Kind: "sheet", Content: raw(sheet(`{"A0":{"v":1}}`))},
		"undeclared alias":  {Kind: "sheet", Content: raw(sheet(`{"A1":{"f":"=AIW_REF(\"X\",\"S!A1\")"}}`))},
		"script node":       {Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"script"}]}}`)},
		"javascript href":   {Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}}`)},
		"remote image":      {Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"image","attrs":{"src":"https://evil/x.png"}}]}}`)},
		"unknown mark":      {Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"onclick"}]}]}]}}`)},
		"duplicate rows":    {Kind: "table", Content: raw(`{"schema":"aiw.table/1","columns":[{"key":"a"}],"rows":[{"id":"r1","cells":{}},{"id":"r1","cells":{}}]}`)},
		"not an object":     {Kind: "board", Content: raw(`[1,2]`)},
		"too large":         {Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[]},"pad":"` + strings.Repeat("x", 1<<20) + `"}`)},
		"unknown kind":      {Kind: "macro"},
		"long title":        {Kind: "doc", Title: strings.Repeat("t", 300)},
	}
	for name, in := range bad {
		if _, err := e.svc.Create(e.ctx, e.me, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	// What the editor produces is accepted: AIW_REF with a declared alias, safe links, embeds, blob images.
	src := e.sheetArt(`{"B12":{"v":1}}`)
	ok := artifacts.CreateInput{Kind: "sheet", Content: raw(`{"schema":"aiw.sheet/1","imports":[{"alias":"IS","artifact_id":"` + src.ID + `"}],"sheets":[{"id":"s1","name":"M","cells":{"A1":{"f":"=AIW_REF(\"IS\",\"Margen!B12\")+SUM(B1:B2)"}}}]}`)}
	if _, err := e.svc.Create(e.ctx, e.me, ok); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, e.me, artifacts.CreateInput{Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[
		{"type":"paragraph","attrs":{"bid":"b1"},"content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"https://ok.example"}},{"type":"artifact_link","attrs":{"artifact_id":"` + src.ID + `"}}]}]},
		{"type":"embed","attrs":{"bid":"b2","artifact_id":"` + src.ID + `"}},{"type":"image","attrs":{"src":"blob:abc"}}]}}`)}); err != nil {
		t.Fatal(err)
	}
}

// ---- versions, merge, conflicts, restore ----

func TestSaveVersionMergesDisjointCellsAndRejectsRealConflicts(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":"Ingresos"},"B1":{"v":100},"B2":{"v":10}}`)
	id := a.ID

	// The agent edits B2 while the human still holds version 1.
	e.attach(id, "accounting", "edit")
	r, err := e.svc.ApplyAgent(e.ctx, "accounting", "task-1", id, artifacts.SaveInput{BaseVersion: 1, Content: raw(sheet(`{"A1":{"v":"Ingresos"},"B1":{"v":100},"B2":{"v":99}}`))})
	if err != nil || r.Version != 2 || r.Merged {
		t.Fatalf("agent save: %v %+v", err, r)
	}
	// The human saves a different cell from the stale base 1: merged, nothing lost.
	r, err = e.svc.Save(e.ctx, e.me, id, artifacts.SaveInput{BaseVersion: 1, Content: raw(sheet(`{"A1":{"v":"Ventas"},"B1":{"v":100},"B2":{"v":10}}`))})
	if err != nil || !r.Merged || r.Version != 3 {
		t.Fatalf("merge: %v %+v", err, r)
	}
	head, _ := e.svc.Get(e.ctx, id, 0)
	if cellV(t, head, "B2") != 99 || !strings.Contains(string(head.Content), `"Ventas"`) {
		t.Fatalf("merged content lost an edit: %s", head.Content)
	}
	if head.LastAuthor.ID != "u1" {
		t.Fatalf("last author: %+v", head.LastAuthor)
	}
	// The same cell changed on both sides: nothing is written, the conflict names the unit.
	_, err = e.svc.Save(e.ctx, e.me, id, artifacts.SaveInput{BaseVersion: 1, Content: raw(sheet(`{"A1":{"v":"Ingresos"},"B1":{"v":100},"B2":{"v":55}}`))})
	var c *artifacts.Conflict
	if !errors.As(err, &c) || c.HeadVersion != 3 || len(c.Conflicts) != 1 || c.Conflicts[0].Unit != "Margen!B2" || c.Conflicts[0].TheirsAuthor == nil || c.Conflicts[0].TheirsAuthor.ID != "u1" {
		t.Fatalf("conflict: %v %+v", err, c)
	}
	if after, _ := e.svc.Get(e.ctx, id, 0); after.HeadVersion != 3 {
		t.Fatal("a conflicting save wrote a version")
	}
	// A save without changes creates no version; a bad base is rejected.
	if r, err := e.svc.Save(e.ctx, e.me, id, artifacts.SaveInput{BaseVersion: 3, Content: head.Content}); err != nil || r.Version != 3 {
		t.Fatalf("no-op save: %v %+v", err, r)
	}
	if _, err := e.svc.Save(e.ctx, e.me, id, artifacts.SaveInput{BaseVersion: 9, Content: head.Content}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("future base: %v", err)
	}
	if _, err := e.svc.Save(e.ctx, e.me, id, artifacts.SaveInput{BaseVersion: 3, Content: raw(`{"schema":"aiw.sheet/1","sheets":[]}`)}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid content: %v", err)
	}
	// History and restore.
	vs := must(e.svc.Versions(e.ctx, id))
	if len(vs) != 3 || vs[0].Version != 3 || vs[0].Source != "rebase" || vs[1].Source != "agent_task" || vs[2].Source != "create" {
		t.Fatalf("versions: %+v", vs)
	}
	if _, err := e.svc.Restore(e.ctx, e.me, id, 1); err != nil {
		t.Fatal(err)
	}
	if now, _ := e.svc.Get(e.ctx, id, 0); now.HeadVersion != 4 || cellV(t, now, "B2") != 10 {
		t.Fatalf("restore: %+v", now.Meta)
	}
	if old, _ := e.svc.Get(e.ctx, id, 2); cellV(t, old, "B2") != 99 {
		t.Fatal("old versions must stay readable")
	}
	if _, err := e.svc.Restore(e.ctx, e.me, id, 77); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("restore of a missing version: %v", err)
	}
	for _, typ := range []string{"artifact.version_created", "artifact.agent_focus"} {
		if e.pub.count(typ) == 0 {
			t.Fatalf("event %s never emitted", typ)
		}
	}
	if ev := e.pub.last("artifact.version_created"); ev["artifact_id"] != id || ev["source"] != "restore" {
		t.Fatalf("last version event: %v", ev)
	}
}

func (e *env) attach(id, agent, mode string) {
	e.t.Helper()
	if _, err := e.svc.Attach(e.ctx, e.me, id, agent, mode); err != nil {
		e.t.Fatal(err)
	}
}

func TestOtherKindsMergePerUnit(t *testing.T) {
	e := newEnv(t, nil)
	doc := e.create(e.me, artifacts.CreateInput{Kind: "doc", Title: "d", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[
		{"type":"paragraph","attrs":{"bid":"b1"},"content":[{"type":"text","text":"uno"}]},{"type":"paragraph","attrs":{"bid":"b2"},"content":[{"type":"text","text":"dos"}]}]}}`)})
	body := func(a, b string) json.RawMessage {
		return raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"paragraph","attrs":{"bid":"b1"},"content":[{"type":"text","text":"` + a + `"}]},{"type":"paragraph","attrs":{"bid":"b2"},"content":[{"type":"text","text":"` + b + `"}]}]}}`)
	}
	if _, err := e.svc.Save(e.ctx, e.me, doc.ID, artifacts.SaveInput{BaseVersion: 1, Content: body("UNO", "dos")}); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.Save(e.ctx, e.me, doc.ID, artifacts.SaveInput{BaseVersion: 1, Content: body("uno", "DOS")})
	if err != nil || !r.Merged {
		t.Fatalf("blocks changed apart must merge: %v %+v", err, r)
	}
	if got := string(r.Content); !strings.Contains(got, "UNO") || !strings.Contains(got, "DOS") {
		t.Fatalf("doc merge: %s", got)
	}
	var c *artifacts.Conflict
	if _, err := e.svc.Save(e.ctx, e.me, doc.ID, artifacts.SaveInput{BaseVersion: 1, Content: body("otro", "dos")}); !errors.As(err, &c) || c.Conflicts[0].Unit != "b1" {
		t.Fatalf("same block: %v", err)
	}

	tbl := func(a, b string) json.RawMessage {
		return raw(`{"schema":"aiw.table/1","columns":[{"key":"risk"},{"key":"sev"}],"rows":[{"id":"r1","cells":{"risk":"` + a + `","sev":"` + b + `"}}]}`)
	}
	tb := e.create(e.me, artifacts.CreateInput{Kind: "table", Title: "t", Content: tbl("p", "Alta")})
	if _, err := e.svc.Save(e.ctx, e.me, tb.ID, artifacts.SaveInput{BaseVersion: 1, Content: tbl("p2", "Alta")}); err != nil {
		t.Fatal(err)
	}
	r, err = e.svc.Save(e.ctx, e.me, tb.ID, artifacts.SaveInput{BaseVersion: 1, Content: tbl("p", "Baja")})
	if err != nil || !r.Merged || !strings.Contains(string(r.Content), "p2") || !strings.Contains(string(r.Content), "Baja") {
		t.Fatalf("table merge per cell: %v %s", err, r.Content)
	}
	if _, err := e.svc.Save(e.ctx, e.me, tb.ID, artifacts.SaveInput{BaseVersion: 1, Content: tbl("p3", "Alta")}); !errors.As(err, &c) || c.Conflicts[0].Unit != "r1.risk" {
		t.Fatalf("same cell: %v", err)
	}

	board := func(extra string) json.RawMessage {
		return raw(`{"schema":"aiw.board/1","view":"kanban","columns":[{"id":"todo","title":"T"}],"cards":[{"id":"k1","col":"todo","title":"uno"}` + extra + `]}`)
	}
	bd := e.create(e.me, artifacts.CreateInput{Kind: "board", Title: "b", Content: board("")})
	if _, err := e.svc.Save(e.ctx, e.me, bd.ID, artifacts.SaveInput{BaseVersion: 1, Content: board(`,{"id":"k2","col":"todo","title":"dos"}`)}); err != nil {
		t.Fatal(err)
	}
	r, err = e.svc.Save(e.ctx, e.me, bd.ID, artifacts.SaveInput{BaseVersion: 1, Content: board(`,{"id":"k3","col":"todo","title":"tres"}`)})
	if err != nil || !strings.Contains(string(r.Content), "k2") || !strings.Contains(string(r.Content), "k3") {
		t.Fatalf("board cards added on both sides: %v %s", err, r.Content)
	}
}

// ---- permissions ----

func TestAgentsNeverApproveAndOnlyWriteWithEditAccess(t *testing.T) {
	e := newEnv(t, nil)
	art := e.sheetArt(`{"A1":{"v":1}}`)
	id := art.ID
	next := func(v int) artifacts.SaveInput {
		return artifacts.SaveInput{BaseVersion: must(e.svc.Get(e.ctx, id, 0)).HeadVersion, Content: raw(sheet(`{"A1":{"v":` + itoa(v) + `}}`))}
	}
	agent := artifacts.Actor{Kind: artifacts.ActorAgent, ID: "legal"}

	// none and read: no write at all; propose: no direct write either.
	for _, mode := range []string{"none", "read", "propose"} {
		e.attach(id, "legal", mode)
		if _, err := e.svc.Save(e.ctx, agent, id, next(2)); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("mode %s must not write: %v", mode, err)
		}
	}
	// read access lets an agent read; none does not.
	e.attach(id, "legal", "none")
	if _, err := e.svc.ReadAsAgent(e.ctx, "legal", id); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("read without access: %v", err)
	}
	e.attach(id, "legal", "read")
	if _, err := e.svc.ReadAsAgent(e.ctx, "legal", id); err != nil {
		t.Fatal(err)
	}
	// edit lets it write, and the version is authored by the agent.
	e.attach(id, "legal", "edit")
	if r, err := e.svc.Save(e.ctx, agent, id, next(3)); err != nil || r.Version != 2 {
		t.Fatalf("edit mode: %v %+v", err, r)
	}
	if h, _ := e.svc.Get(e.ctx, id, 0); h.LastAuthor.Kind != "agent" || h.LastAuthor.ID != "legal" {
		t.Fatalf("author: %+v", h.LastAuthor)
	}
	// Agents never approve, send, archive, attach, or unlock - even with edit access.
	for _, st := range []string{"approved", "sent", "archived"} {
		s := st
		if _, err := e.svc.Patch(e.ctx, agent, id, artifacts.PatchInput{Status: &s}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("agent set status %s: %v", st, err)
		}
	}
	if _, err := e.svc.Attach(e.ctx, agent, id, "legal", "edit"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an agent changed attachments: %v", err)
	}
	inReview := "in_review"
	if _, err := e.svc.Patch(e.ctx, agent, id, artifacts.PatchInput{Status: &inReview}); err != nil {
		t.Fatalf("an agent may ask for review: %v", err)
	}
	// There is no approve mode, and unknown agents/modes are refused.
	if _, err := e.svc.Attach(e.ctx, e.admin, id, "legal", "approve"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("approve mode: %v", err)
	}
	if _, err := e.svc.Attach(e.ctx, e.me, id, "ghost", "read"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	if _, err := e.svc.Attach(e.ctx, e.me, id, "legal", "root"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
	// A person without the approve permission cannot approve either; one with it can, and that locks the artifact.
	approved := "approved"
	if _, err := e.svc.Patch(e.ctx, e.me, id, artifacts.PatchInput{Status: &approved}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member approving: %v", err)
	}
	m, err := e.svc.Patch(e.ctx, e.admin, id, artifacts.PatchInput{Status: &approved})
	if err != nil || !m.Locked || m.Status != "approved" {
		t.Fatalf("admin approving: %v %+v", err, m)
	}
	if _, err := e.svc.Save(e.ctx, e.admin, id, next(4)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a locked artifact accepted a write: %v", err)
	}
	if _, err := e.svc.Save(e.ctx, agent, id, next(4)); err == nil {
		t.Fatal("an agent wrote to a locked artifact")
	}
	if _, err := e.svc.Restore(e.ctx, e.admin, id, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("restore on a locked artifact: %v", err)
	}
	draft := "draft"
	if _, err := e.svc.Patch(e.ctx, e.me, id, artifacts.PatchInput{Status: &draft}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member unlocked an approved artifact: %v", err)
	}
	if m, err := e.svc.Patch(e.ctx, e.admin, id, artifacts.PatchInput{Status: &draft}); err != nil || m.Locked {
		t.Fatalf("unlock: %v %+v", err, m)
	}
	// Archiving: the creator may, someone else needs delete permission.
	other := artifacts.Actor{Kind: artifacts.ActorUser, ID: "u2"}
	if err := e.svc.Archive(e.ctx, other, id); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("archive by a stranger: %v", err)
	}
	if err := e.svc.Archive(e.ctx, e.me, id); err != nil {
		t.Fatal(err)
	}
	if list := must(e.svc.List(e.ctx, artifacts.Filter{})); len(list) != 0 {
		t.Fatalf("archived artifacts must leave the list: %d", len(list))
	}
	if e.pub.count("artifact.status_changed") < 3 || e.pub.count("artifact.deleted") != 1 {
		t.Fatal("status events missing")
	}
	// An agent creating an artifact owns it with edit access.
	own := e.create(agent, artifacts.CreateInput{Kind: "doc", Title: "mine"})
	if own.CreatedBy.Kind != "agent" || len(own.Attachments) != 1 || own.Attachments[0].Mode != "edit" {
		t.Fatalf("agent-created artifact: %+v", own.Meta)
	}
	if _, err := e.svc.Create(e.ctx, artifacts.Actor{Kind: artifacts.ActorAgent, ID: "ghost"}, artifacts.CreateInput{Kind: "doc"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown agent create: %v", err)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// ---- links and dependencies ----

func TestSheetImportsRecalculateWhenTheSourceChanges(t *testing.T) {
	e := newEnv(t, nil)
	income := e.create(e.me, artifacts.CreateInput{Kind: "sheet", Title: "IS", Content: raw(`{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Result","cells":{"B12":{"v":112000},"B1":{"v":10}}}]}`)})
	balance := e.create(e.me, artifacts.CreateInput{Kind: "sheet", Title: "BS", Content: raw(`{"schema":"aiw.sheet/1","imports":[{"alias":"IS","artifact_id":"` + income.ID + `"}],
		"sheets":[{"id":"s1","name":"Balance","cells":{"B1":{"v":100},"B2":{"f":"=AIW_REF(\"IS\",\"Result!B12\")","v":112000},"B3":{"f":"=B1+B2","v":112100},"B4":{"f":"=ROUND(B3/3,0)","v":37367}}}]}`)})
	if len(balance.DependsOnArtifact) != 1 || balance.DependsOnArtifact[0] != income.ID {
		t.Fatalf("depends_on_artifacts: %+v", balance.DependsOnArtifact)
	}
	links := must(e.svc.Links(e.ctx, balance.ID, "out"))
	if len(links) != 1 || links[0].Relation != "source_of" || links[0].Stale || links[0].Alias == nil || *links[0].Alias != "IS" || links[0].Anchor == nil || links[0].Anchor.Range != "B12" {
		t.Fatalf("links: %+v", links)
	}
	if in := must(e.svc.Links(e.ctx, income.ID, "in")); len(in) != 1 || in[0].From != balance.ID {
		t.Fatalf("incoming links: %+v", in)
	}
	// The source changes: the dependent is recalculated (B2 and what depends on it).
	if _, err := e.svc.Save(e.ctx, e.me, income.ID, artifacts.SaveInput{BaseVersion: 1, Content: raw(`{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Result","cells":{"B12":{"v":200000},"B1":{"v":10}}}]}`)}); err != nil {
		t.Fatal(err)
	}
	b, _ := e.svc.Get(e.ctx, balance.ID, 0)
	if b.HeadVersion != 2 || cellV(t, b, "B2") != 200000 || cellV(t, b, "B3") != 200100 || cellV(t, b, "B4") != 66700 {
		t.Fatalf("recalc: v%d B2=%v B3=%v B4=%v", b.HeadVersion, cellV(t, b, "B2"), cellV(t, b, "B3"), cellV(t, b, "B4"))
	}
	vs := must(e.svc.Versions(e.ctx, balance.ID))
	if vs[0].Source != "recalc" || vs[0].Author.Kind != "system" {
		t.Fatalf("recalc version: %+v", vs[0])
	}
	if e.pub.count("artifact.dependency_changed") < 2 || e.pub.count("artifact.recalculated") != 1 {
		t.Fatalf("dependency events: changed=%d recalculated=%d", e.pub.count("artifact.dependency_changed"), e.pub.count("artifact.recalculated"))
	}
	if l := must(e.svc.Links(e.ctx, balance.ID, "out")); l[0].Stale {
		t.Fatal("the link is still stale after the recalculation")
	}
}

func TestManualRefreshAndStaleLinksWithoutAutoRecalc(t *testing.T) {
	e := newEnv(t, func(c *artifacts.Config) { c.DisableAutoRecalc = true })
	src := e.sheetArt(`{"B2":{"v":5}}`)
	dep := e.create(e.me, artifacts.CreateInput{Kind: "sheet", Title: "dep", Content: raw(`{"schema":"aiw.sheet/1","imports":[{"alias":"S","artifact_id":"` + src.ID + `"}],
		"sheets":[{"id":"s1","name":"D","cells":{"A1":{"f":"=AIW_REF(\"S\",\"Margen!B2\")*2","v":10}}}]}`)})
	if _, err := e.svc.Save(e.ctx, e.me, src.ID, artifacts.SaveInput{BaseVersion: 1, Content: raw(sheet(`{"B2":{"v":50}}`))}); err != nil {
		t.Fatal(err)
	}
	if l := must(e.svc.Links(e.ctx, dep.ID, "out")); !l[0].Stale {
		t.Fatal("the dependent must be stale until it is refreshed")
	}
	if got, _ := e.svc.Get(e.ctx, dep.ID, 0); got.HeadVersion != 1 {
		t.Fatal("no auto recalc was configured")
	}
	res, err := e.svc.RefreshDependencies(e.ctx, e.me, dep.ID)
	if err != nil || !res.Refreshed || res.Version != 2 || len(res.ChangedUnits) != 1 || res.ChangedUnits[0] != "D!A1" {
		t.Fatalf("refresh: %v %+v", err, res)
	}
	if got, _ := e.svc.Get(e.ctx, dep.ID, 0); cellV(t, got, "A1") != 100 {
		t.Fatalf("A1 = %v", cellV(t, got, "A1"))
	}
	if l := must(e.svc.Links(e.ctx, dep.ID, "out")); l[0].Stale {
		t.Fatal("still stale after refresh")
	}
	if res, err := e.svc.RefreshDependencies(e.ctx, e.me, dep.ID); err != nil || res.Refreshed {
		t.Fatalf("a second refresh must be a no-op: %v %+v", err, res)
	}
	// Manual links.
	if _, err := e.svc.AddLink(e.ctx, e.me, dep.ID, src.ID, "refers_to", &artifacts.Anchor{Sheet: "Margen", Range: "B2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddLink(e.ctx, e.me, dep.ID, src.ID, "refers_to", nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate link: %v", err)
	}
	if _, err := e.svc.AddLink(e.ctx, e.me, dep.ID, dep.ID, "refers_to", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("self link: %v", err)
	}
	if _, err := e.svc.AddLink(e.ctx, e.me, dep.ID, "art_nope", "refers_to", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("link to a missing artifact: %v", err)
	}
	if _, err := e.svc.AddLink(e.ctx, e.me, dep.ID, src.ID, "owns", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown relation: %v", err)
	}
}

// ---- isolation, ask, workspace ----

func TestOrganizationIsolation(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	other := application.WithOrg(context.Background(), "00000000-0000-0000-0000-0000000000b2")
	if _, err := e.svc.Get(other, a.ID, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("read across organizations: %v", err)
	}
	if list := must(e.svc.List(other, artifacts.Filter{})); len(list) != 0 {
		t.Fatalf("list across organizations: %d", len(list))
	}
	ops := map[string]func() error{
		"save": func() error {
			_, err := e.svc.Save(other, e.admin, a.ID, artifacts.SaveInput{BaseVersion: 1, Content: a.Content})
			return err
		},
		"patch": func() error {
			t := "x"
			_, err := e.svc.Patch(other, e.admin, a.ID, artifacts.PatchInput{Title: &t})
			return err
		},
		"restore": func() error { _, err := e.svc.Restore(other, e.admin, a.ID, 1); return err },
		"attach":  func() error { _, err := e.svc.Attach(other, e.admin, a.ID, "legal", "read"); return err },
		"links":   func() error { _, err := e.svc.Links(other, a.ID, "both"); return err },
		"refresh": func() error { _, err := e.svc.RefreshDependencies(other, e.admin, a.ID); return err },
		"version": func() error { _, err := e.svc.Versions(other, a.ID); return err },
		"archive": func() error { return e.svc.Archive(other, e.admin, a.ID) },
	}
	for name, fn := range ops {
		if err := fn(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s across organizations: %v", name, err)
		}
	}
	// A reference to an artifact of another organization is not a link.
	foreign := must(e.svc.Create(other, e.admin, artifacts.CreateInput{Kind: "doc", Title: "f"}))
	linked := e.create(e.me, artifacts.CreateInput{Kind: "doc", Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"embed","attrs":{"bid":"b","artifact_id":"` + foreign.ID + `"}}]}}`)})
	if len(must(e.svc.Links(e.ctx, linked.ID, "out"))) != 0 {
		t.Fatal("a link crossed organizations")
	}
	if len(must(e.svc.List(e.ctx, artifacts.Filter{}))) != 2 {
		t.Fatal("the foreign artifact leaked into this organization")
	}
}

func TestAskAnAgentGrantsReadAndGoesThroughTheOrchestrator(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	reqID, err := e.svc.Ask(e.ctx, e.me, a.ID, artifacts.AskInput{AgentID: "legal", Mode: "review_my_changes"})
	if err != nil || reqID != "req-1" || len(e.ask.texts) != 1 || !strings.Contains(e.ask.texts[0], a.ID) {
		t.Fatalf("ask: %v %v %v", err, reqID, e.ask.texts)
	}
	if got, _ := e.svc.Get(e.ctx, a.ID, 0); len(got.Attachments) != 1 || got.Attachments[0].Mode != "read" {
		t.Fatalf("asked agent must get read access only: %+v", got.Attachments)
	}
	for name, in := range map[string]artifacts.AskInput{
		"free without text": {AgentID: "legal", Mode: "free"}, "bad mode": {AgentID: "legal", Mode: "x"},
	} {
		if _, err := e.svc.Ask(e.ctx, e.me, a.ID, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.Ask(e.ctx, e.me, a.ID, artifacts.AskInput{AgentID: "ghost", Mode: "free", Text: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

type lookup struct{}

func (lookup) Info(_ context.Context, id string) (string, string, error) {
	if id == "prj-1" {
		return "Cierre", "running", nil
	}
	return "", "", domain.ErrNotFound
}

func TestProjectWorkspaceFollowsTheNodesOfAProject(t *testing.T) {
	e := newEnv(t, func(c *artifacts.Config) { c.Projects = lookup{} })
	ev := func(node, agent, state string, progress int, out *domain.StructuredOutput) projects.NodeEvent {
		return projects.NodeEvent{ProjectID: "prj-1", ProjectName: "Cierre", NodeID: node, Title: "Paso " + node, AgentID: agent, State: state, Progress: progress, TaskID: "t-" + node, RequestID: "rq", Output: out}
	}
	e.svc.NodeChanged(e.ctx, ev("n1", "accounting", "pending", 0, nil))
	e.svc.NodeChanged(e.ctx, ev("n2", "accounting", "pending", 0, nil)) // two deliverables of the same agent in parallel
	e.svc.NodeChanged(e.ctx, ev("n3", "legal", "pending", 0, nil))
	e.svc.NodeChanged(e.ctx, ev("n1", "accounting", "running", 50, nil))
	ws, err := e.svc.Workspace(e.ctx, "prj-1")
	if err != nil || ws.Name != "Cierre" || len(ws.Deliverables) != 3 {
		t.Fatalf("workspace: %v %+v", err, ws)
	}
	byID := map[string]artifacts.Deliverable{}
	for _, d := range ws.Deliverables {
		byID[d.DeliverableID] = d
	}
	if d := byID["n1"]; d.BuildState != "building" || d.Progress != 50 || d.AgentID != "accounting" || len(d.Artifacts) != 1 {
		t.Fatalf("n1: %+v", d)
	}
	if byID["n2"].BuildState != "queued" || byID["n3"].Artifacts[0].Kind != "doc" || byID["n1"].Artifacts[0].Kind != "sheet" {
		t.Fatalf("queued deliverables or kinds by role: %+v", byID)
	}
	// Finishing writes the task output as a version authored by the agent.
	e.svc.NodeChanged(e.ctx, ev("n1", "accounting", "done", 100, &domain.StructuredOutput{Summary: "Balance listo", Metrics: map[string]any{"activos": 1200, "nota": "ok"}}))
	e.svc.NodeChanged(e.ctx, ev("n3", "legal", "done", 100, &domain.StructuredOutput{Summary: "Contrato revisado", Findings: []string{"Cláusula 7 excede la política"}}))
	ws, _ = e.svc.Workspace(e.ctx, "prj-1")
	for _, d := range ws.Deliverables {
		if d.DeliverableID == "n1" || d.DeliverableID == "n3" {
			a := d.Artifacts[0]
			if a.BuildState != "ready_for_review" || a.Progress != 100 || a.HeadVersion != 2 || a.LastAuthor.Kind != "agent" {
				t.Fatalf("%s after done: %+v", d.DeliverableID, a)
			}
		}
	}
	n1 := byID["n1"].Artifacts[0]
	full, _ := e.svc.Get(e.ctx, n1.ID, 0)
	if !strings.Contains(string(full.Content), "activos") || !strings.Contains(string(full.Content), "1200") {
		t.Fatalf("output not written: %s", full.Content)
	}
	if e.pub.count("artifact.progress_changed") == 0 || e.pub.count("artifact.created") != 3 {
		t.Fatalf("events: progress=%d created=%d", e.pub.count("artifact.progress_changed"), e.pub.count("artifact.created"))
	}
	if _, err := e.svc.Workspace(e.ctx, "prj-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace of an unknown project: %v", err)
	}
	// Replaying the same node event is idempotent (no duplicate deliverables).
	e.svc.NodeChanged(e.ctx, ev("n2", "accounting", "pending", 0, nil))
	if ws, _ = e.svc.Workspace(e.ctx, "prj-1"); len(ws.Deliverables) != 3 {
		t.Fatalf("duplicate deliverables: %d", len(ws.Deliverables))
	}
	// Another organization has an empty workspace for the same project id.
	other := application.WithOrg(context.Background(), "00000000-0000-0000-0000-0000000000b2")
	if ws, err := e.svc.Workspace(other, "prj-1"); err != nil || len(ws.Deliverables) != 0 {
		t.Fatalf("workspace across organizations: %v %+v", err, ws.Deliverables)
	}
}
