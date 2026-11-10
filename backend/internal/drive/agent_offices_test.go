package drive_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

// fakeAgentOffices maps an agent to its office ("" = General / none).
type fakeAgentOffices map[string]string

func (f fakeAgentOffices) AgentOffice(_ context.Context, _, agentID string) (string, error) {
	return f[agentID], nil
}

// officePeopleByID answers drive.PeopleAccess per office (unknown office: none).
type officePeopleByID map[string]string

func (f officePeopleByID) OfficeAccess(_ context.Context, _, _, officeID string) (string, error) {
	if l, ok := f[officeID]; ok {
		return l, nil
	}
	return drive.AccessNone, nil
}

// newOfficeEnv is newAgentEnv with two offices: Contabilidad (agent acct) and Legal (agent legal), a file in each.
func newOfficeEnv(t *testing.T) *agentEnv {
	t.Helper()
	e := newAgentEnv(t)
	if _, err := e.svc.EnsureOfficeFolder(e.ctx, "dep_acct", "Contabilidad", []string{"Clientes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EnsureOfficeFolder(e.ctx, "dep_legal", "Legal", []string{"Casos"}); err != nil {
		t.Fatal(err)
	}
	h := drive.Human("u")
	for p, c := range map[string]string{
		"/Offices/Contabilidad/Clientes/balance.md": "balance trimestral confidencial",
		"/Offices/Legal/Casos/demanda.md":           "demanda confidencial del caso",
	} {
		if _, err := e.svc.Write(e.ctx, h, drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
			t.Fatal(err)
		}
	}
	e.fs.Offices = fakeAgentOffices{"acct": "dep_acct", "legal": "dep_legal"}
	return e
}

func TestAgentWritesOwnOfficeWithoutApproval(t *testing.T) {
	e := newOfficeEnv(t)
	pl := e.plan("acct", "write", map[string]any{"path": "/Offices/Contabilidad/Clientes/a.md", "content": "hola"})
	if pl.Deny != "" || pl.Approval {
		t.Fatalf("own office write: %+v", pl)
	}
	if r := e.fs.Run(e.ctx, "acct", "write", map[string]any{"path": "/Offices/Contabilidad/Clientes/a.md", "content": "hola"}, false); !r.OK {
		t.Fatalf("run: %+v", r)
	}
	// the store ignores case: the own office in lower case is still the own office
	if pl := e.plan("acct", "write", map[string]any{"path": "/offices/contabilidad/b.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("own office (lower case) write: %+v", pl)
	}
	if pl := e.plan("acct", "read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}); pl.Deny != "" {
		t.Fatalf("own office read: %+v", pl)
	}
	// another office's folder: approval, never free
	if pl := e.plan("acct", "write", map[string]any{"path": "/Offices/Legal/Casos/x.md", "content": "x"}); pl.Deny == "" && !pl.Approval {
		t.Fatalf("other office write must not be free: %+v", pl)
	}
}

func TestAgentCannotReadOtherOffice(t *testing.T) {
	e := newOfficeEnv(t)
	for _, p := range []string{"/Offices/Legal/Casos/demanda.md", "/offices/legal/casos/demanda.md", "/OFFICES/Legal/Casos/demanda.md"} {
		if pl := e.plan("acct", "read", map[string]any{"path": p}); pl.Deny != "not_granted" {
			t.Fatalf("read %s must be refused: %+v", p, pl)
		}
	}
	for _, p := range []string{"/Offices/Legal", "/offices/legal/Casos"} {
		if pl := e.plan("acct", "list", map[string]any{"path": p}); pl.Deny != "not_granted" {
			t.Fatalf("list %s must be refused: %+v", p, pl)
		}
	}
	if pl := e.plan("acct", "search", map[string]any{"query": "confidencial", "path": "/offices/legal"}); pl.Deny != "not_granted" {
		t.Fatalf("search under another office must be refused: %+v", pl)
	}
	r := e.fs.Run(e.ctx, "acct", "search", map[string]any{"query": "confidencial"}, false)
	if !r.OK || strings.Contains(strings.ToLower(r.Text), "legal") || !strings.Contains(r.Text, "balance.md") {
		t.Fatalf("search: %+v", r)
	}
	// /Offices lists only the agent's own office
	r = e.fs.Run(e.ctx, "acct", "list", map[string]any{"path": "/Offices"}, false)
	if !r.OK || strings.Contains(r.Text, "Legal") || !strings.Contains(r.Text, "/Offices/Contabilidad") {
		t.Fatalf("list /Offices: %+v", r)
	}
	// recent files: nothing of Legal by name
	r = e.fs.Run(e.ctx, "acct", "recent", map[string]any{"by": "anyone"}, false)
	if !r.OK || strings.Contains(r.Text, "demanda") || !strings.Contains(r.Text, "balance.md") {
		t.Fatalf("recent: %+v", r)
	}
	// an agent without an office (General) reads no office
	if pl := e.plan("sales", "read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}); pl.Deny != "not_granted" {
		t.Fatalf("an agent of General must not read an office: %+v", pl)
	}
}

// passage search: hits of another office are dropped even when the index returns them.
type leakyChunks struct{ hits []drive.ChunkHit }

func (l leakyChunks) SearchChunks(_ context.Context, _ drive.ChunkQuery) (drive.ChunkResult, error) {
	return drive.ChunkResult{Hits: l.hits, Mode: "keyword"}, nil
}

func TestAgentChunkSearchDropsOtherOffice(t *testing.T) {
	e := newOfficeEnv(t)
	e.fs.Chunks = leakyChunks{hits: []drive.ChunkHit{
		{Path: "/Offices/Legal/Casos/demanda.md", Snippet: "demanda"},
		{Path: "/offices/legal/Casos/demanda.md", Snippet: "demanda"},
		{Path: "/Offices/Contabilidad/Clientes/balance.md", Snippet: "balance"},
	}}
	r := e.fs.Run(e.ctx, "acct", "search", map[string]any{"query": "confidencial", "mode": "keyword"}, false)
	if !r.OK || strings.Contains(strings.ToLower(r.Text), "legal") || !strings.Contains(r.Text, "balance.md") {
		t.Fatalf("chunk search: %+v", r)
	}
}

func TestAgentLimitedByRequester(t *testing.T) {
	e := newOfficeEnv(t)
	people := officePeopleByID{"dep_acct": drive.AccessNone}
	e.svc.SetPeople(people)
	beto := drive.WithRequester(e.ctx, "beto")
	bal := "/Offices/Contabilidad/Clientes/balance.md"

	if pl := e.fs.Plan(beto, "acct", "read", map[string]any{"path": bal}); pl.Deny != "person_no_access" {
		t.Fatalf("beto has no access to Contabilidad: the agent must not read for him: %+v", pl)
	}
	if pl := e.fs.Plan(beto, "acct", "read", map[string]any{"path": "/offices/contabilidad/Clientes/balance.md"}); pl.Deny != "person_no_access" {
		t.Fatalf("lower case must not evade the person's limit: %+v", pl)
	}
	r := e.fs.Run(beto, "acct", "search", map[string]any{"query": "confidencial"}, false)
	if !r.OK || strings.Contains(r.Text, "balance") {
		t.Fatalf("search for beto must not show Contabilidad: %+v", r)
	}
	r = e.fs.Run(beto, "acct", "recent", map[string]any{"by": "anyone"}, false)
	if !r.OK || strings.Contains(r.Text, "balance") {
		t.Fatalf("recent for beto must not show Contabilidad: %+v", r)
	}
	roots, err := e.fs.ReadableRoots(beto, "acct")
	if err != nil || drive.InScope(roots, bal) {
		t.Fatalf("readable roots for beto: %v %v", roots, err)
	}
	// without a person (a scheduled run), the agent keeps its own office
	if pl := e.fs.Plan(e.ctx, "acct", "read", map[string]any{"path": bal}); pl.Deny != "" {
		t.Fatalf("no requester: %+v", pl)
	}

	people["dep_acct"] = drive.AccessRead
	if pl := e.fs.Plan(beto, "acct", "read", map[string]any{"path": bal}); pl.Deny != "" {
		t.Fatalf("beto reads Contabilidad: so does the agent: %+v", pl)
	}
	if pl := e.fs.Plan(beto, "acct", "write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}); pl.Deny != "" || !pl.Approval {
		t.Fatalf("beto only reads: the write needs approval: %+v", pl)
	}

	people["dep_acct"] = drive.AccessEdit
	if pl := e.fs.Plan(beto, "acct", "write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("beto edits Contabilidad: the write is free: %+v", pl)
	}
}

func TestAgentGrantToOtherOfficeLimitedByRequester(t *testing.T) {
	e := newOfficeEnv(t)
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("admin"), "acct", "/Offices/Legal", drive.AccessRead); err != nil {
		t.Fatal(err)
	}
	doc := "/Offices/Legal/Casos/demanda.md"
	if pl := e.plan("acct", "read", map[string]any{"path": doc}); pl.Deny != "" {
		t.Fatalf("a Read rule opens Legal: %+v", pl)
	}
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessEdit, "dep_legal": drive.AccessNone})
	beto := drive.WithRequester(e.ctx, "beto")
	if pl := e.fs.Plan(beto, "acct", "read", map[string]any{"path": doc}); pl.Deny != "person_no_access" {
		t.Fatalf("the rule never goes beyond the person: %+v", pl)
	}
	r := e.fs.Run(beto, "acct", "search", map[string]any{"query": "confidencial"}, false)
	if !r.OK || strings.Contains(r.Text, "demanda") || !strings.Contains(r.Text, "balance.md") {
		t.Fatalf("search for beto: %+v", r)
	}
}

func TestReadableRootsIncludeOwnOffice(t *testing.T) {
	e := newOfficeEnv(t)
	roots, err := e.fs.ReadableRoots(e.ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(roots, "/Offices/Contabilidad") || slices.ContainsFunc(roots, func(r string) bool { return strings.Contains(strings.ToLower(r), "legal") }) {
		t.Fatalf("roots = %v", roots)
	}
	if roots, _ := e.fs.ReadableRoots(e.ctx, "sales"); slices.ContainsFunc(roots, func(r string) bool { return strings.HasPrefix(r, "/Offices") }) {
		t.Fatalf("an agent of General has no office folder: %v", roots)
	}
	e.fs.Offices = nil
	if roots, _ := e.fs.ReadableRoots(e.ctx, "acct"); slices.ContainsFunc(roots, func(r string) bool { return strings.HasPrefix(r, "/Offices") }) {
		t.Fatalf("without Offices there is no office folder: %v", roots)
	}
}

// A Ninguno rule inside the agent's own office holds whatever the case of the path (the store ignores case).
func TestAgentOfficeNoneRuleIgnoresCase(t *testing.T) {
	e := newOfficeEnv(t)
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("admin"), "acct", "/Offices/Contabilidad/Clientes", drive.AccessNone); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/Offices/Contabilidad/Clientes/balance.md", "/offices/contabilidad/clientes/balance.md"} {
		if pl := e.plan("acct", "read", map[string]any{"path": p}); pl.Deny != "not_granted" {
			t.Fatalf("read %s must be refused: %+v", p, pl)
		}
	}
	if pl := e.plan("acct", "write", map[string]any{"path": "/offices/contabilidad/clientes/n.md", "content": "x"}); pl.Deny == "" && !pl.Approval {
		t.Fatalf("a Ninguno folder is not free to write: %+v", pl)
	}
}

// When the limit comes from the person (none), every action there is refused with its own code, and the message never
// offers to give the agent access (that would not help: the person cannot see the folder).
func TestPersonNoAccessIsRefusedNotApproved(t *testing.T) {
	e := newOfficeEnv(t)
	if _, err := e.svc.Write(e.ctx, drive.AgentP("acct"), drive.WriteInput{Path: "/Agents/acct/x.md", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessNone})
	beto := drive.WithRequester(e.ctx, "beto")
	bal := "/Offices/Contabilidad/Clientes/balance.md"
	for _, c := range []struct {
		action string
		args   map[string]any
	}{
		{"write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}},
		{"write_document", map[string]any{"path": "/Offices/Contabilidad/x.md", "document": map[string]any{"title": "x"}}},
		{"edit", map[string]any{"path": bal, "expected_version": 1, "ops": []any{}}},
		{"move", map[string]any{"path": "/Agents/acct/x.md", "to": "/Offices/Contabilidad/x.md"}},
		{"move", map[string]any{"path": bal, "to": "/Agents/acct/b.md"}},
		{"delete", map[string]any{"path": bal}},
		{"read", map[string]any{"path": bal}},
		{"list", map[string]any{"path": "/Offices/Contabilidad/Clientes"}},
		{"search", map[string]any{"query": "balance", "path": "/Offices/Contabilidad"}},
		{"recent", map[string]any{"path": "/Offices/Contabilidad"}},
	} {
		pl := e.fs.Plan(beto, "acct", c.action, c.args)
		if pl.Deny != "person_no_access" || pl.Approval || strings.Contains(pl.DenyMsg, "give you access") {
			t.Fatalf("%s %v: %+v", c.action, c.args["path"], pl)
		}
	}
	if r := e.fs.Run(beto, "acct", "recent", map[string]any{"path": "/Offices/Contabilidad"}, false); r.Code != "person_no_access" {
		t.Fatalf("recent run: %+v", r)
	}
}

// Only the platform itself (no requester, the scheduler, the system...) leaves the agent its office: it works on its
// own there (spec §4.1). An actor that is neither a person nor the platform (a token, a link, Slack, another agent)
// acts for someone the drive cannot tell, so inside /Offices the agent gets nothing for it (fails closed).
func TestSystemRequesterKeepsOwnOfficeForeignGetsNone(t *testing.T) {
	e := newOfficeEnv(t)
	e.svc.SetPeople(officePeopleByID{})
	for _, who := range []string{"", "orchestrator", "demo", "runtime", "scheduler", "system", "user"} {
		ctx := drive.WithRequester(e.ctx, who)
		if pl := e.fs.Plan(ctx, "acct", "write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}); pl.Deny != "" || pl.Approval {
			t.Fatalf("%q: the agent keeps its office: %+v", who, pl)
		}
		if pl := e.fs.Plan(ctx, "acct", "read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}); pl.Deny != "" {
			t.Fatalf("%q: reads its office: %+v", who, pl)
		}
		if roots, _ := e.fs.ReadableRoots(ctx, "acct"); !slices.Contains(roots, "/Offices/Contabilidad") {
			t.Fatalf("%q: roots %v", who, roots)
		}
	}
	for _, who := range []string{"token:abc", "slack:U1", "agent:legal", "link:x", "Some One"} {
		ctx := drive.WithRequester(e.ctx, who)
		if pl := e.fs.Plan(ctx, "acct", "write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}); pl.Deny != "person_no_access" {
			t.Fatalf("%s: no office for a foreign requester (write): %+v", who, pl)
		}
		if pl := e.fs.Plan(ctx, "acct", "read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}); pl.Deny != "person_no_access" {
			t.Fatalf("%s: no office for a foreign requester (read): %+v", who, pl)
		}
		roots, _ := e.fs.ReadableRoots(ctx, "acct")
		if slices.Contains(roots, "/Offices/Contabilidad") || drive.InScope(roots, "/Offices/Contabilidad/x.md") || !drive.InScope(roots, "/Shared/x.md") {
			t.Fatalf("%s: roots %v", who, roots)
		}
	}
}

// fs.recent "no access" never names a folder under /Offices the person cannot see.
func TestRecentNoAccessHidesOfficesThePersonCannotSee(t *testing.T) {
	e := newOfficeEnv(t)
	beto := drive.Human("beto")
	for _, p := range []string{"/Offices/Legal/Casos/subido.md", "/Projects/Otro/subido.md"} {
		if _, err := e.svc.Write(e.ctx, beto, drive.WriteInput{Path: p, Data: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessEdit, "dep_legal": drive.AccessNone})
	r := e.fs.Run(drive.WithRequester(e.ctx, "beto"), "acct", "recent", map[string]any{}, false)
	if !r.OK || !strings.Contains(r.Text, "no access: 1 recent file(s) of this person in /Projects/Otro") || strings.Contains(r.Text, "Legal") {
		t.Fatalf("recent: %+v", r)
	}
	// a person who can read Legal (an open office) still hears about it
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessEdit, "dep_legal": drive.AccessRead})
	r = e.fs.Run(drive.WithRequester(e.ctx, "beto"), "acct", "recent", map[string]any{}, false)
	if !r.OK || !strings.Contains(r.Text, "/Offices/Legal/Casos") {
		t.Fatalf("recent (beto reads Legal): %+v", r)
	}
}

// linkedCustomers links folders (by id) to customers.
type linkedCustomers map[string]string

func (linkedCustomers) ProjectCustomer(context.Context, string, string) (string, error) {
	return "", nil
}
func (l linkedCustomers) LinkedFolders(context.Context, string) (map[string]string, error) {
	return l, nil
}

// A customer only narrows: a customer folder linked inside another office never opens to an agent of another office
// (or of General) in a task for that customer; the agent of that office reads it.
func TestCustomerTaskDoesNotOpenAnotherOffice(t *testing.T) {
	e := newOfficeEnv(t)
	acme := "/Offices/Contabilidad/Clientes/ACME"
	if _, err := e.svc.Write(e.ctx, drive.Human("u"), drive.WriteInput{Path: acme + "/estado.md", Data: []byte("estado de cuenta de acme")}); err != nil {
		t.Fatal(err)
	}
	l, err := e.svc.List(e.ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	e.fs.Customers = linkedCustomers{l.Folder.ID: "cus_acme"}
	ctx := application.WithCustomer(e.ctx, "cus_acme")
	file := acme + "/estado.md"
	for _, agent := range []string{"legal", "sales"} {
		for _, p := range []string{file, strings.ToLower(file)} {
			if pl := e.fs.Plan(ctx, agent, "read", map[string]any{"path": p}); pl.Deny == "" {
				t.Fatalf("%s reads %s: %+v", agent, p, pl)
			}
		}
		if pl := e.fs.Plan(ctx, agent, "list", map[string]any{"path": acme}); pl.Deny == "" {
			t.Fatalf("%s lists ACME: %+v", agent, pl)
		}
		if pl := e.fs.Plan(ctx, agent, "write", map[string]any{"path": acme + "/n.md", "content": "x"}); pl.Deny == "" {
			t.Fatalf("%s writes ACME: %+v", agent, pl)
		}
		for _, action := range []string{"search", "recent"} {
			r := e.fs.Run(ctx, agent, action, map[string]any{"query": "acme", "by": "anyone"}, false)
			if !r.OK || strings.Contains(r.Text, "estado") || strings.Contains(r.Text, "ACME") {
				t.Fatalf("%s %s: %+v", agent, action, r)
			}
		}
		r := e.fs.Run(ctx, agent, "list", map[string]any{"path": "/Offices"}, false)
		if strings.Contains(r.Text, "ACME") {
			t.Fatalf("%s list /Offices: %+v", agent, r)
		}
		if roots, _ := e.fs.ReadableRoots(ctx, agent); drive.InScope(roots, file) {
			t.Fatalf("%s roots: %v", agent, roots)
		}
		f, _ := e.fs.ReadableFilter(ctx, agent)
		if f(file) {
			t.Fatalf("%s filter reads ACME", agent)
		}
	}
	if pl := e.fs.Plan(ctx, "acct", "read", map[string]any{"path": file}); pl.Deny != "" {
		t.Fatalf("the agent of Contabilidad reads its customer folder: %+v", pl)
	}
	if roots, _ := e.fs.ReadableRoots(ctx, "acct"); !drive.InScope(roots, file) {
		t.Fatalf("acct roots: %v", roots)
	}
}

// Through the Tool Gateway, a read the person cannot make is a soft result: the agent gets the code and the message as
// data (so it can tell the person), never a policy block. A write stays refused.
func TestGatewayPersonNoAccessReadIsSoft(t *testing.T) {
	e := newOfficeEnv(t)
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessNone})
	tool := &drive.GatewayTool{FS: e.fs, AgentTools: func(context.Context, string, string) ([]string, error) { return []string{"fs"}, nil }}
	for _, c := range []struct {
		action string
		args   map[string]any
	}{
		{"read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}},
		{"list", map[string]any{"path": "/Offices/Contabilidad/Clientes"}},
		{"search", map[string]any{"query": "balance", "path": "/Offices/Contabilidad"}},
		{"recent", map[string]any{"path": "/Offices/Contabilidad"}},
	} {
		call := application.GatewayCall{Org: "o1", AgentID: "acct", TaskID: "t1", OnBehalfOf: "beto", Tool: "fs", Action: c.action, Args: c.args}
		if as := tool.Assess(e.ctx, call); as.DenyCode != "" || as.NeedsApproval {
			t.Fatalf("%s: a person limit on a read is not a policy block: %+v", c.action, as)
		}
		res := tool.Run(e.ctx, call, false)
		if res.Status != "succeeded" || !strings.Contains(res.Summary, "person_no_access") || len(res.Blocks) != 1 ||
			!strings.Contains(res.Blocks[0], "the person you work for has no access") || strings.Contains(res.Blocks[0], "balance trimestral") {
			t.Fatalf("%s: run: %+v", c.action, res)
		}
	}
	w := application.GatewayCall{Org: "o1", AgentID: "acct", TaskID: "t1", OnBehalfOf: "beto", Tool: "fs", Action: "write",
		Args: map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}}
	if as := tool.Assess(e.ctx, w); as.DenyCode != "person_no_access" {
		t.Fatalf("a write stays refused: %+v", as)
	}
}

// With nobody to work for, an agent writes nothing into another office (nor creates one): refused, not approved.
func TestNoPersonOtherOfficeWriteIsRefused(t *testing.T) {
	e := newOfficeEnv(t)
	for _, a := range []struct{ action, path string }{
		{"write", "/Offices/Legal/Casos/x.md"}, {"write", "/Offices/Nueva/x.md"}, {"write", "/Offices/x.md"},
		{"write_document", "/Offices/Legal/x.md"}, {"write_document", "/Offices/Nueva/x.md"},
	} {
		args := map[string]any{"path": a.path, "content": "x"}
		if a.action == "write_document" {
			args = map[string]any{"path": a.path, "document": map[string]any{"title": "x", "blocks": []any{map[string]any{"type": "paragraph", "text": "x"}}}}
		}
		if pl := e.plan("acct", a.action, args); pl.Deny != "other_office" || pl.Approval {
			t.Fatalf("%s %s: %+v", a.action, a.path, pl)
		}
	}
	// a move from its office into another office (or a new one) too
	for _, to := range []string{"/Offices/Legal/Casos/balance.md", "/Offices/Nueva/balance.md"} {
		if pl := e.plan("acct", "move", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md", "to": to}); pl.Deny != "other_office" {
			t.Fatalf("move to %s: %+v", to, pl)
		}
	}
	// its own office, its desk and /Shared are unchanged
	if pl := e.plan("acct", "write", map[string]any{"path": "/Offices/Contabilidad/x.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("own office: %+v", pl)
	}
	if pl := e.plan("acct", "write", map[string]any{"path": "/Shared/x.md", "content": "x"}); pl.Deny != "" || !pl.Approval {
		t.Fatalf("shared: %+v", pl)
	}
	// a rule a person set that opens the other office still decides (read: writes ask a human)
	if _, err := e.svc.AddGrant(e.ctx, drive.Human("u"), "acct", "/Offices/Legal"); err != nil {
		t.Fatal(err)
	}
	if pl := e.plan("acct", "write", map[string]any{"path": "/Offices/Legal/x.md", "content": "x"}); pl.Deny != "" || !pl.Approval {
		t.Fatalf("other office with a read rule: %+v", pl)
	}
	// with a person who edits there, the write is theirs to approve
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessEdit, "dep_legal": drive.AccessEdit})
	if pl := e.fs.Plan(drive.WithRequester(e.ctx, "beto"), "acct", "write", map[string]any{"path": "/Offices/Legal/y.md", "content": "x"}); pl.Deny != "" || !pl.Approval {
		t.Fatalf("person who edits Legal: %+v", pl)
	}
}

// The agent's zones follow the store: /OFFİCES/... (dotted capital İ) is /Offices for Postgres and the memstore alike.
func TestAgentZonesUnicodeSpellingOfOffices(t *testing.T) {
	e := newOfficeEnv(t)
	const legal, own = "/OFFİCES/Legal/Casos/demanda.md", "/OFFİCES/Contabilidad/z.md"
	if pl := e.plan("acct", "read", map[string]any{"path": legal}); pl.Deny != "not_granted" {
		t.Fatalf("read another office: %+v", pl)
	}
	if pl := e.plan("acct", "write", map[string]any{"path": "/OFFİCES/Legal/x.md", "content": "x"}); pl.Deny != "other_office" {
		t.Fatalf("write another office: %+v", pl)
	}
	if pl := e.plan("acct", "write", map[string]any{"path": own, "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("own office: %+v", pl)
	}
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessNone})
	beto := drive.WithRequester(e.ctx, "beto")
	if pl := e.fs.Plan(beto, "acct", "write", map[string]any{"path": own, "content": "x"}); pl.Deny != "person_no_access" {
		t.Fatalf("person without access, unicode spelling: %+v", pl)
	}
	if pl := e.fs.Plan(beto, "acct", "read", map[string]any{"path": "/OFFİCES/Contabilidad/Clientes/balance.md"}); pl.Deny != "person_no_access" {
		t.Fatalf("person without access, unicode read: %+v", pl)
	}
}
