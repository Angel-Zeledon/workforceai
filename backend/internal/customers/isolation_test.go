package customers_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/objstore"
)

// Cross-customer leakage (B12): a task scoped to customer A must never recall, search, open, list, read or write
// anything of customer B, through memory recall, memory.search, fs.* or the drive leg of the memory.

const org = "o1"

type assigned map[string][]drive.ProjectRef

func (p assigned) AgentProjects(_ context.Context, _, agentID string) ([]drive.ProjectRef, error) {
	return p[agentID], nil
}

type driveFolders struct{ d *drive.Service }

func (f driveFolders) EnsureCustomerFolder(ctx context.Context, id, name string) (string, string, error) {
	fl, err := f.d.EnsureOwnedFolder(ctx, drive.RootClients, id, name)
	return fl.ID, fl.Path, err
}

func (f driveFolders) FolderPath(ctx context.Context, id string) (string, error) {
	fl, err := f.d.FolderByID(ctx, id)
	return fl.Path, err
}

type fakeWork struct{ projects map[string]string } // project id -> customer

func (w fakeWork) Projects(context.Context, string) ([]customers.ProjectInfo, error) { return nil, nil }
func (w fakeWork) ProjectCustomer(_ context.Context, _, id string) (string, error) {
	return w.projects[id], nil
}
func (w fakeWork) Request(context.Context, string, string) (customers.RequestInfo, error) {
	return customers.RequestInfo{}, domain.ErrNotFound
}

type world struct {
	ctx    context.Context
	cust   *customers.Service
	drv    *drive.Service
	fs     *drive.AgentFS
	mem    *memory.Service
	memGW  *memory.GatewayTool
	fsGW   *drive.GatewayTool
	a, b   customers.Customer
	pathsA []string
	pathsB []string
}

const agent = "accounting"

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := application.WithOrg(context.Background(), org)
	drv := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org})
	cust := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org})
	cust.SetFolders(driveFolders{drv})
	cust.SetWork(fakeWork{projects: map[string]string{}})
	w := &world{ctx: ctx, cust: cust, drv: drv}
	var err error
	if w.a, err = cust.Create(ctx, "ana", customers.Input{Name: ptr("Ferretería López")}); err != nil {
		t.Fatal(err)
	}
	if w.b, err = cust.Create(ctx, "ana", customers.Input{Name: ptr("Panadería San José")}); err != nil {
		t.Fatal(err)
	}
	if w.a.FolderPath == "" || w.b.FolderPath == "" {
		t.Fatalf("customers must get a drive folder: %+v %+v", w.a, w.b)
	}
	cust.SetWork(fakeWork{projects: map[string]string{"prj_a": w.a.ID, "prj_b": w.b.ID}})
	for _, p := range []struct{ id, name string }{{"prj_a", "Cierre Lopez"}, {"prj_b", "Cierre Panaderia"}, {"prj_g", "General"}} {
		if _, err := drv.EnsureOwnedFolder(ctx, drive.RootProjects, p.id, p.name); err != nil {
			t.Fatal(err)
		}
	}
	w.pathsA = []string{w.a.FolderPath + "/balance.md", "/Projects/Cierre Lopez/conciliacion.md", drive.CustomerDesk(agent, w.a.ID) + "/borrador.md"}
	w.pathsB = []string{w.b.FolderPath + "/balance.md", "/Projects/Cierre Panaderia/conciliacion.md", drive.CustomerDesk(agent, w.b.ID) + "/borrador.md",
		"/Shared/Panaderia/precios.md",   // linked to B below: deeper than /Shared, so it is B's
		"/Agents/" + agent + "/notas.md", // the general desk: may hold anyone's data
		"/Projects/General/resumen.md",   // an unscoped project
	}
	h := drive.Human("ana")
	for _, p := range w.pathsA {
		if _, err := drv.Write(ctx, h, drive.WriteInput{Path: p, Data: []byte("saldo de bancos ferreteria clavos tornillos 111")}); err != nil {
			t.Fatal(p, err)
		}
	}
	for _, p := range w.pathsB {
		if _, err := drv.Write(ctx, h, drive.WriteInput{Path: p, Data: []byte("saldo de bancos panaderia harina levadura 222")}); err != nil {
			t.Fatal(p, err)
		}
	}
	if _, err := drv.Write(ctx, h, drive.WriteInput{Path: "/Shared/politica.md", Data: []byte("politica de saldo de bancos del despacho")}); err != nil {
		t.Fatal(err)
	}
	sub, err := drv.FolderByPath(ctx, "/Shared/Panaderia")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cust.LinkFolder(ctx, "ana", w.b.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	for req, c := range map[string]string{"rq-a1": w.a.ID, "rq-a2": w.a.ID, "rq-b1": w.b.ID} {
		if err := cust.LinkRequest(ctx, org, req, c); err != nil {
			t.Fatal(err)
		}
	}

	w.fs = &drive.AgentFS{Svc: drv, Projects: assigned{agent: {{ID: "prj_a"}, {ID: "prj_b"}, {ID: "prj_g"}}}, Customers: cust}
	docs := memory.NewMemStore()
	w.mem = memory.New(memory.Config{Docs: docs, CustomerOf: cust.CustomerOfRequest,
		Retriever: &memory.Multi{Retrievers: []memory.Retriever{&memory.FTS{Docs: docs, Files: &memory.DriveFiles{FS: w.fs}}}}})
	w.memGW = &memory.GatewayTool{Svc: w.mem, CustomerOf: cust.CustomerOfRequest}
	w.fsGW = &drive.GatewayTool{FS: w.fs, CustomerOf: cust.CustomerOfRequest}

	fin := time.Now().UTC()
	task := func(id, req, summary string, notes ...string) domain.Task {
		return domain.Task{ID: id, RequestID: req, AgentID: agent, Title: "Conciliación de saldo de bancos", Status: domain.TaskDone, FinishedAt: &fin,
			Output: &domain.StructuredOutput{Summary: summary, MemoryNotes: notes}}
	}
	w.mem.RememberTask(ctx, org, task("t-a", "rq-a1", "saldo de bancos ferreteria cuadra con 111"), "conciliar bancos ferreteria")
	w.mem.RememberTask(ctx, org, task("t-b", "rq-b1", "saldo de bancos panaderia cuadra con 222", "La panaderia paga el saldo de bancos los lunes"), "conciliar bancos panaderia")
	w.mem.RememberTask(ctx, org, task("t-g", "rq-general", "saldo de bancos de todos los clientes panaderia y ferreteria"), "resumen general")
	w.mem.RememberReport(ctx, org, domain.Report{ID: "r-b", RequestID: "rq-b1", Title: "Informe saldo de bancos panaderia", Summary: "saldo 222", CreatedAt: fin})
	if err := w.mem.RememberNote(ctx, org, agent, "Recordar: el saldo de bancos se concilia cada mes"); err != nil {
		t.Fatal(err)
	}
	return w
}

func ptr(s string) *string { return &s }

func leaks(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "panaderia") || strings.Contains(s, "222") || strings.Contains(s, "harina")
}

func TestRecallNeverCrossesCustomers(t *testing.T) {
	w := newWorld(t)
	got := w.mem.Recall(w.ctx, org, application.MemoryRecall{AgentID: agent, RequestID: "rq-a2", Text: "saldo de bancos conciliacion"})
	refs := map[string]bool{}
	for _, m := range got {
		refs[m.Ref] = true
		if leaks(m.Title + " " + m.Snippet + " " + m.Ref) {
			t.Fatalf("customer A recalled customer B's data: %+v", m)
		}
	}
	if !refs["task:t-a"] {
		t.Fatalf("A's own work must be recalled: %+v", got)
	}
	if refs["task:t-g"] {
		t.Fatalf("unscoped work may hold any customer's data: it must not reach a scoped task: %+v", got)
	}
	foundNote := false
	for r := range refs {
		foundNote = foundNote || strings.HasPrefix(r, "memory:"+memory.UserNotePrefix)
	}
	if !foundNote {
		t.Fatalf("a note a person wrote for the agent is firm-wide and must be recalled: %+v", got)
	}

	// The same agent without a customer keeps seeing everything (backward compatible).
	all := w.mem.Recall(w.ctx, org, application.MemoryRecall{AgentID: agent, RequestID: "rq-x", Text: "saldo de bancos panaderia ferreteria"})
	seen := map[string]bool{}
	for _, m := range all {
		seen[m.Ref] = true
	}
	if !seen["task:t-b"] || !seen["task:t-a"] {
		t.Fatalf("an unscoped task must keep the old recall: %+v", all)
	}
}

func TestMemorySearchAndOpenNeverCrossCustomers(t *testing.T) {
	w := newWorld(t)
	call := application.GatewayCall{Org: org, AgentID: agent, TaskID: "t-new", RequestID: "rq-a2", Tool: memory.ToolName, Action: memory.ActSearch,
		Args: map[string]any{"query": "saldo de bancos panaderia harina"}}
	res := w.memGW.Run(w.ctx, call, false)
	if res.Status != "succeeded" || leaks(strings.Join(res.Blocks, "\n")) {
		t.Fatalf("memory.search of A leaked B: %+v", res)
	}
	call.Args = map[string]any{"ref": "task:t-b"}
	if res := w.memGW.Run(w.ctx, call, false); !strings.Contains(strings.Join(res.Blocks, ""), "not found") || leaks(strings.Join(res.Blocks, "")) {
		t.Fatalf("opening B's memory from A must fail: %+v", res)
	}
	call.Args = map[string]any{"ref": "report:r-b"}
	if res := w.memGW.Run(w.ctx, call, false); leaks(strings.Join(res.Blocks, "")) {
		t.Fatalf("opening B's report from A must fail: %+v", res)
	}
	// B sees its own.
	call.RequestID, call.Args = "rq-b1", map[string]any{"ref": "task:t-b"}
	if res := w.memGW.Run(w.ctx, call, false); !strings.Contains(strings.Join(res.Blocks, ""), "panaderia") {
		t.Fatalf("B must open its own memory: %+v", res)
	}
}

func TestDriveNeverCrossesCustomers(t *testing.T) {
	w := newWorld(t)
	ctxA := application.WithCustomer(w.ctx, w.a.ID)
	for _, p := range w.pathsB {
		if pl := w.fs.Plan(ctxA, agent, drive.ActRead, map[string]any{"path": p}); pl.Deny != "not_granted" {
			t.Fatalf("A must not read %s: %+v", p, pl)
		}
		if pl := w.fs.Plan(ctxA, agent, drive.ActWrite, map[string]any{"path": p, "content": "x"}); pl.Deny != "customer_isolation" {
			t.Fatalf("A must never write %s: %+v", p, pl)
		}
	}
	for _, p := range w.pathsA {
		if pl := w.fs.Plan(ctxA, agent, drive.ActRead, map[string]any{"path": p}); pl.Deny != "" {
			t.Fatalf("A must read its own %s: %+v", p, pl)
		}
	}
	if pl := w.fs.Plan(ctxA, agent, drive.ActRead, map[string]any{"path": "/Shared/politica.md"}); pl.Deny != "" {
		t.Fatalf("/Shared stays readable: %+v", pl)
	}
	if pl := w.fs.Plan(ctxA, agent, drive.ActWrite, map[string]any{"path": "/Shared/copia.md", "content": "saldo 111"}); pl.Deny != "customer_isolation" {
		t.Fatalf("a scoped task must not copy its customer's data where others read: %+v", pl)
	}
	if pl := w.fs.Plan(ctxA, agent, drive.ActWrite, map[string]any{"path": drive.CustomerDesk(agent, w.a.ID) + "/nuevo.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("the clone's desk is free to write: %+v", pl)
	}
	if pl := w.fs.Plan(ctxA, agent, drive.ActMove, map[string]any{"path": w.pathsA[0], "to": w.b.FolderPath + "/robado.md"}); pl.Deny != "customer_isolation" {
		t.Fatalf("moving A's file into B's folder must be refused: %+v", pl)
	}

	// fs.search and fs.list through the gateway, scoped by the request id (not by ctx).
	call := application.GatewayCall{Org: org, AgentID: agent, TaskID: "t", RequestID: "rq-a2", Tool: drive.ToolName, Action: drive.ActSearch,
		Args: map[string]any{"query": "saldo bancos"}}
	res := w.fsGW.Run(w.ctx, call, false)
	text := strings.Join(res.Blocks, "\n")
	if res.Status != "succeeded" || leaks(text) || !strings.Contains(text, "ferreteria") {
		t.Fatalf("fs.search of A must see only A (and /Shared): %+v", res)
	}
	for _, p := range []string{"/", "/Shared", "/Clients"} {
		call.Action, call.Args = drive.ActList, map[string]any{"path": p}
		res = w.fsGW.Run(w.ctx, call, false)
		if out := strings.Join(res.Blocks, "\n"); strings.Contains(out, "Panader") {
			t.Fatalf("fs.list %s of A named B's folders: %s", p, out)
		}
	}
	call.Action, call.Args = drive.ActRead, map[string]any{"path": w.pathsB[0]}
	if res := w.fsGW.Run(w.ctx, call, false); leaks(strings.Join(res.Blocks, "")) {
		t.Fatalf("fs.read of B from A leaked: %+v", res)
	}

	// The drive leg of the memory: files of B never come back to A.
	files, err := (&memory.DriveFiles{FS: w.fs}).Files(ctxA, org, agent, []string{"saldo", "bancos", "panaderia"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if leaks(f.Path + f.Snippet) {
			t.Fatalf("memory files of A included B: %+v", f)
		}
	}

	// Without a customer the old policy holds: the general desk is readable, other clients are not.
	if pl := w.fs.Plan(w.ctx, agent, drive.ActRead, map[string]any{"path": "/Agents/" + agent + "/notas.md"}); pl.Deny != "" {
		t.Fatalf("unscoped tasks keep their desk: %+v", pl)
	}
	if pl := w.fs.Plan(w.ctx, agent, drive.ActRead, map[string]any{"path": w.pathsB[0]}); pl.Deny != "not_granted" {
		t.Fatalf("client folders stay closed without a grant: %+v", pl)
	}
}

func TestUnknownScopeFailsClosed(t *testing.T) {
	w := newWorld(t)
	broken := func(context.Context, string, string) (string, error) { return "", errors.New("db down") }
	mem := memory.New(memory.Config{Docs: memory.NewMemStore(), CustomerOf: broken,
		Retriever: &memory.FTS{Docs: memory.NewMemStore()}})
	if got := mem.Recall(w.ctx, org, application.MemoryRecall{AgentID: agent, RequestID: "rq-a2", Text: "saldo"}); got != nil {
		t.Fatalf("an unknown scope must recall nothing: %+v", got)
	}
	gw := &drive.GatewayTool{FS: w.fs, CustomerOf: broken}
	call := application.GatewayCall{Org: org, AgentID: agent, RequestID: "rq-a2", Tool: drive.ToolName, Action: drive.ActRead, Args: map[string]any{"path": "/Shared/politica.md"}}
	if as := gw.Assess(w.ctx, call); as.DenyCode != "customer_scope_unknown" {
		t.Fatalf("fs must refuse when the scope is unknown: %+v", as)
	}
	mgw := &memory.GatewayTool{Svc: w.mem, CustomerOf: broken}
	if res := mgw.Run(w.ctx, application.GatewayCall{Org: org, AgentID: agent, RequestID: "rq-a2", Args: map[string]any{"query": "saldo"}}, false); res.Code != "customer_scope_unknown" {
		t.Fatalf("memory.search must refuse when the scope is unknown: %+v", res)
	}
	// A scoped call on an AgentFS without a customer directory is refused too.
	bare := &drive.AgentFS{Svc: w.drv}
	if pl := bare.Plan(application.WithCustomer(w.ctx, w.a.ID), agent, drive.ActRead, map[string]any{"path": "/Shared/politica.md"}); pl.Deny != "drive_unavailable" {
		t.Fatalf("no directory: fail closed: %+v", pl)
	}
}
