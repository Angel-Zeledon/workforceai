package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	infmem "aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/memory"
)

// callsIn returns the position of the first call in fn whose function is named name (a plain call or a method).
func callsIn(fn *ast.FuncDecl, name string) (token.Pos, int) {
	var first token.Pos
	n := 0
	ast.Inspect(fn, func(x ast.Node) bool {
		c, ok := x.(*ast.CallExpr)
		if !ok {
			return true
		}
		var got string
		switch f := c.Fun.(type) {
		case *ast.Ident:
			got = f.Name
		case *ast.SelectorExpr:
			got = f.Sel.Name
		}
		if got == name {
			if n == 0 {
				first = c.Pos()
			}
			n++
		}
		return true
	})
	return first, n
}

func funcDecl(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Recv == nil {
			return fd
		}
	}
	t.Fatalf("%s not found in %s", name, file)
	return nil
}

// D3 (I1): everything that seals a department (the office scope of the memory, the approver policy, who receives the
// live events, the connections, the drive and the skills of an office) is installed BEFORE durable execution resumes
// the requests a previous process left in progress; otherwise the resumed work emits unsealed events, posts its
// approvals to company channels and writes its memory as organization memory.
func TestDepartmentsAreWiredBeforeRecover(t *testing.T) {
	run := funcDecl(t, "main.go", "run")
	wire, n := callsIn(run, "wireDepartments")
	if n != 1 {
		t.Fatalf("run calls wireDepartments %d times, want 1", n)
	}
	rec, n := callsIn(run, "Recover")
	if n == 0 {
		t.Fatal("run does not resume in-progress requests (Recover)")
	}
	if wire > rec {
		t.Fatal("wireDepartments runs after orch.Recover: resumed work would run unsealed")
	}
	for _, name := range []string{"wireOffices", "wireApprovers", "officeRecall", "SetAudienceFor", "SetAgentOffices", "SetPeople"} {
		if _, n := callsIn(run, name); n != 0 {
			t.Errorf("run calls %s directly: it belongs in wireDepartments (before Recover)", name)
		}
	}
	dep := funcDecl(t, "offices.go", "wireDepartments")
	for _, name := range []string{"wireOffices", "wireApprovers", "officeRecall", "SetAudienceFor", "SetAgentOffices", "SetPeople", "SetOffices"} {
		if _, n := callsIn(dep, name); n == 0 {
			t.Errorf("wireDepartments does not call %s", name)
		}
	}
}

// wireDepartments installs the approver policy on the approvals the orchestrator decides with.
func TestWireDepartmentsInstallsTheApproverPolicy(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := infmem.New()
	rec := &application.Recorder{Store: store, Log: log}
	approvals := application.NewApprovals(application.DefaultConfig(), store, rec)
	orch := application.NewOrchestrator(ctx, application.DefaultConfig(), store, nil, infmem.NewLocker(), rec, approvals, nil, log)
	mem := memory.New(memory.Config{Docs: memory.NewMemStore(), Log: log})
	svc := wireDepartments(departmentWiring{AuthOn: true, Store: store, AuthStore: auth.NewMemoryStore(), Rec: rec, Memory: mem, Orch: orch, Log: log})
	if svc == nil {
		t.Fatal("no offices service")
	}
	if approvals.Policy() == nil {
		t.Fatal("the approver policy is not installed")
	}
}
