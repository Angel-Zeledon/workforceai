// Package memorytest is the contract suite every memory.DocStore (and the
// drive behind it) must pass: ranking sanity, accent-insensitive Spanish,
// organization isolation, private notes, drive permissions and deletions.
// The in-memory store and Postgres (with Row-Level Security) run the same suite.
package memorytest

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/memory"
)

type projects map[string][]drive.ProjectRef

func (p projects) AgentProjects(_ context.Context, _, agentID string) ([]drive.ProjectRef, error) {
	return p[agentID], nil
}

type audited struct{ actions []string }

func done(id, req, agent, title, summary string, findings ...string) domain.Task {
	fin := time.Now().UTC()
	return domain.Task{ID: id, RequestID: req, AgentID: agent, Title: title, Status: domain.TaskDone, FinishedAt: &fin,
		Output: &domain.StructuredOutput{Summary: summary, Findings: findings}}
}

// Run executes the suite. drv must be backed by the same kind of store as docs.
func Run(t *testing.T, docs memory.DocStore, drv *drive.Service, orgA, orgB string) {
	t.Helper()
	au := &audited{}
	fs := &drive.AgentFS{Svc: drv, Projects: projects{"sales": {{ID: "prj_1", Name: "Plan"}}}}
	fts := &memory.FTS{Docs: docs, Files: &memory.DriveFiles{FS: fs}}
	svc := memory.New(memory.Config{Docs: docs, Retriever: &memory.Multi{Retrievers: []memory.Retriever{fts}},
		ProjectOf: func(_ context.Context, _, req string) (string, string) {
			if strings.HasPrefix(req, "rq-plan") {
				return "prj_1", "Plan"
			}
			return "", ""
		},
		Audit: func(_ context.Context, _, actor, action, id string, _ map[string]any) {
			au.actions = append(au.actions, actor+":"+action+":"+id)
		}})
	ctxA, ctxB := application.WithOrg(context.Background(), orgA), application.WithOrg(context.Background(), orgB)

	// ---- documents: ranking, Spanish without accents, figures ----
	svc.RememberTask(ctxA, orgA, done("t1", "rq-old", "finance", "Planeación financiera del trimestre", "Proyección de ventas por $50,000 con margen del 30%", "El margen mejora con descuentos por volumen"), "Necesito una proyección de ventas")
	svc.RememberTask(ctxA, orgA, done("t2", "rq-old", "finance", "Revisión de contratos", "Contrato de arrendamiento sin cláusulas de riesgo"), "Revisar el contrato")
	svc.RememberTask(ctxA, orgA, done("t3", "rq-plan-1", "finance", "Presupuesto del plan", "Presupuesto de ventas para el plan comercial"), "Plan comercial")
	svc.RememberTask(ctxA, orgA, done("t4", "rq-other", "finance", "Presupuesto de oficina", "Presupuesto de ventas de papelería"), "Compras de oficina")
	svc.RememberReport(ctxA, orgA, domain.Report{ID: "r1", RequestID: "rq-old", Title: "Informe de ventas", Summary: "Las ventas crecieron", CreatedAt: time.Now().UTC()})

	t.Run("accent insensitive spanish", func(t *testing.T) {
		got, err := svc.Search(ctxA, orgA, "sales", "planeacion financiera", 6)
		if err != nil || len(got) == 0 || got[0].Ref != "task:t1" {
			t.Fatalf("planeacion must find the task written with accent first: %v %+v", err, got)
		}
	})
	t.Run("figures match however they are written", func(t *testing.T) {
		got, _ := svc.Search(ctxA, orgA, "sales", "ventas de 50000", 6)
		if len(got) == 0 || got[0].Ref != "task:t1" {
			t.Fatalf("50000 must find $50,000: %+v", got)
		}
	})
	t.Run("ranking sanity", func(t *testing.T) {
		got, _ := svc.Search(ctxA, orgA, "sales", "contrato de arrendamiento", 6)
		if len(got) == 0 || got[0].Ref != "task:t2" {
			t.Fatalf("the contract task must rank first: %+v", got)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Fatalf("results must be sorted by score: %+v", got)
			}
		}
		if n, _ := svc.Search(ctxA, orgA, "sales", "zzzz qqqq inexistente", 6); len(n) != 0 {
			t.Fatalf("no match, no results: %+v", n)
		}
	})
	t.Run("same project preferred", func(t *testing.T) {
		got := svc.Recall(ctxA, orgA, application.MemoryRecall{AgentID: "sales", RequestID: "rq-plan-2", Text: "presupuesto de ventas"})
		if len(got) < 2 || got[0].Ref != "task:t3" || got[0].Project != "Plan" {
			t.Fatalf("the task of the same project must come first: %+v", got)
		}
	})
	t.Run("own request excluded and budget respected", func(t *testing.T) {
		got := svc.Recall(ctxA, orgA, application.MemoryRecall{AgentID: "sales", RequestID: "rq-old", Text: "ventas contrato planeación presupuesto informe"})
		chars := 0
		for _, m := range got {
			if m.Ref == "task:t1" || m.Ref == "task:t2" || m.Ref == "report:r1" {
				t.Fatalf("documents of the same request must not be recalled: %+v", m)
			}
			chars += len(m.Title) + len(m.Snippet)
		}
		if len(got) > memory.MaxItems || chars > memory.MaxChars+200 {
			t.Fatalf("budget exceeded: %d items %d chars", len(got), chars)
		}
	})

	// ---- notes: private to the agent, secrets never stored ----
	nt := done("t5", "rq-old", "sales", "Cierre de venta", "Cliente cerrado")
	nt.Output.MemoryNotes = []string{"Acme siempre paga a 45 días, confirmar la fecha antes de enviar la factura",
		"password: hunter2hunter2 del portal", "x", "Acme siempre paga a 45 días, confirmar la fecha antes de enviar la factura"}
	svc.RememberTask(ctxA, orgA, nt, "Cerrar la venta de Acme")
	t.Run("notes are private and clean", func(t *testing.T) {
		own, _ := svc.Search(ctxA, orgA, "sales", "Acme factura 45 días", 6)
		if len(own) == 0 || own[0].Kind != memory.KindNote || own[0].SourceRef != "task:t5" {
			t.Fatalf("the note must be recalled with its source: %+v", own)
		}
		other, _ := svc.Search(ctxA, orgA, "legal", "Acme factura 45 días", 6)
		for _, it := range other {
			if it.Kind == memory.KindNote {
				t.Fatalf("a note is private to the agent that wrote it: %+v", it)
			}
		}
		l, _ := svc.List(ctxA, orgA, "sales", 50)
		notes := 0
		for _, it := range l {
			if it.Kind == memory.KindNote {
				notes++
				if strings.Contains(it.Snippet, "hunter2") {
					t.Fatal("a secret must never be remembered")
				}
			}
		}
		if notes != 1 {
			t.Fatalf("one clean note expected (secret, too short and duplicate dropped): %+v", l)
		}
	})

	// ---- deletion: audited, only the agent's own, survives a re-index ----
	t.Run("delete is audited and sticks", func(t *testing.T) {
		l, _ := svc.List(ctxA, orgA, "sales", 50)
		var id string
		for _, it := range l {
			if it.Kind == memory.KindNote {
				id = it.ID
			}
		}
		if err := svc.Delete(ctxA, orgA, "legal", id, "ana"); err == nil {
			t.Fatal("another agent's memory cannot be deleted")
		}
		if err := svc.Delete(ctxB, orgB, "sales", id, "eve"); err == nil {
			t.Fatal("another organization cannot delete it")
		}
		if err := svc.Delete(ctxA, orgA, "sales", id, "ana"); err != nil {
			t.Fatal(err)
		}
		svc.RememberTask(ctxA, orgA, nt, "Cerrar la venta de Acme") // re-index / backfill
		if got, _ := svc.Search(ctxA, orgA, "sales", "Acme factura 45 días", 6); len(got) != 0 && got[0].Kind == memory.KindNote {
			t.Fatalf("a deleted memory must not come back: %+v", got)
		}
		if len(au.actions) != 1 || au.actions[0] != "ana:memory.deleted:"+id {
			t.Fatalf("audit: %v", au.actions)
		}
	})

	// ---- organization isolation ----
	t.Run("organization isolation", func(t *testing.T) {
		svc.RememberTask(ctxB, orgB, done("tb", "rq-b", "sales", "Secreto de la otra empresa", "Planeación financiera confidencial de B"), "x")
		for _, q := range []string{"planeacion financiera", "secreto empresa"} {
			a, _ := svc.Search(ctxA, orgA, "sales", q, 10)
			for _, it := range a {
				if it.Ref == "task:tb" {
					t.Fatalf("org A saw a document of org B: %+v", it)
				}
			}
		}
		b, _ := svc.Search(ctxB, orgB, "sales", "planeacion financiera", 10)
		if len(b) != 1 || b[0].Ref != "task:tb" {
			t.Fatalf("org B must only see its own: %+v", b)
		}
		if _, err := docs.Get(ctxB, orgB, "task_t1"); err == nil {
			t.Fatal("Get must not cross organizations")
		}
	})

	// ---- drive files: only what the agent may read ----
	t.Run("drive permissions", func(t *testing.T) {
		if _, err := drv.EnsureOwnedFolder(ctxA, drive.RootProjects, "prj_1", "Plan"); err != nil {
			t.Fatal(err)
		}
		if _, err := drv.EnsureOwnedFolder(ctxA, drive.RootProjects, "prj_2", "Otro"); err != nil {
			t.Fatal(err)
		}
		h := drive.Human("u")
		for p, c := range map[string]string{
			"/Agents/sales/notas.md": "zafiro: notas de escritorio propio", "/Agents/legal/ajeno.md": "zafiro: escritorio de legal",
			"/Projects/Plan/plan.md": "zafiro: plan del proyecto asignado", "/Projects/Otro/otro.md": "zafiro: otro proyecto",
			"/Shared/politica.md": "zafiro: política compartida", "/Clients/Acme/contrato.md": "zafiro: contrato de Acme",
		} {
			if _, err := drv.Write(ctxA, h, drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
				t.Fatal(err)
			}
		}
		paths := func(agent string) []string {
			items, err := svc.Search(ctxA, orgA, agent, "zafiro", 12)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, it := range items {
				if it.Kind == memory.KindFile {
					out = append(out, strings.TrimPrefix(it.Ref, "file:"))
				}
			}
			return out
		}
		got := strings.Join(paths("sales"), ",")
		for _, ok := range []string{"/Agents/sales/notas.md", "/Projects/Plan/plan.md", "/Shared/politica.md"} {
			if !strings.Contains(got, ok) {
				t.Fatalf("sales must retrieve %s: %s", ok, got)
			}
		}
		for _, no := range []string{"/Agents/legal/ajeno.md", "/Projects/Otro/otro.md", "/Clients/Acme/contrato.md"} {
			if strings.Contains(got, no) {
				t.Fatalf("sales must NEVER retrieve %s: %s", no, got)
			}
		}
		if g := strings.Join(paths("marketing"), ","); strings.Contains(g, "/Agents/sales") || strings.Contains(g, "/Projects/Plan") || strings.Contains(g, "Acme") {
			t.Fatalf("an agent without assignment or grant sees only its desk and /Shared: %s", g)
		}
		if _, err := drv.AddGrant(ctxA, h, "sales", "/Clients/Acme"); err != nil {
			t.Fatal(err)
		}
		if g := strings.Join(paths("sales"), ","); !strings.Contains(g, "/Clients/Acme/contrato.md") {
			t.Fatalf("a human grant opens reading: %s", g)
		}
		// the same folder in another organization is invisible
		if other, _ := svc.Search(ctxB, orgB, "sales", "zafiro", 12); len(other) != 0 {
			for _, it := range other {
				if it.Kind == memory.KindFile {
					t.Fatalf("org B must not see files of org A: %+v", it)
				}
			}
		}
		// a deleted file leaves the memory at once
		f, _ := drv.FileByPath(ctxA, "/Shared/politica.md")
		if err := drv.DeleteFile(ctxA, h, f.ID); err != nil {
			t.Fatal(err)
		}
		if g := strings.Join(paths("sales"), ","); strings.Contains(g, "politica.md") {
			t.Fatalf("a deleted file must not be recalled: %s", g)
		}
	})

	t.Run("open by ref respects privacy", func(t *testing.T) {
		if it, err := svc.Open(ctxA, orgA, "sales", "task:t1"); err != nil || !strings.Contains(it.Snippet, "50000") && !strings.Contains(it.Snippet, "50,000") {
			t.Fatalf("open task: %v %+v", err, it)
		}
		if _, err := svc.Open(ctxB, orgB, "sales", "task:t1"); err == nil {
			t.Fatal("open must not cross organizations")
		}
		if _, err := svc.Open(ctxA, orgA, "sales", "memory:t5.1"); err == nil {
			t.Fatal("a deleted note cannot be opened")
		}
	})
}
