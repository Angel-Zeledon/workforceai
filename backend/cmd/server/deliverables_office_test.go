package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/projects"
)

func TestDeliverableHonorsTheRequestedFormat(t *testing.T) {
	ctx := context.Background()
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "org1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	sink := deliverableSink{drive: svc, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	out := &domain.StructuredOutput{Summary: "Catalogo listo.", Findings: []string{"10 productos", "=SUM(A1)"}, Recommendations: []string{"Revisar precios"}, Metrics: map[string]any{"productos": 10}}
	for _, c := range []struct{ name, title, ext string }{
		{"Hazme un Excel con 10 productos", "Catálogo", ".xlsx"},
		{"Informe de ventas en PDF", "Informe", ".pdf"},
		{"Prepara un documento de Word con el plan", "Plan", ".docx"},
	} {
		sink.NodeChanged(ctx, projects.NodeEvent{ProjectID: "p-" + c.ext, ProjectName: c.name, NodeID: "p-" + c.ext + ":t1", Kind: projects.KindTask, Title: c.title,
			AgentID: "analyst", State: projects.StateDone, Output: out})
		folder, err := svc.EnsureOwnedFolder(ctx, drive.RootProjects, "p-"+c.ext, c.name)
		if err != nil {
			t.Fatal(err)
		}
		l, err := svc.List(ctx, folder.Path)
		if err != nil {
			t.Fatal(err)
		}
		var office, md bool
		for _, f := range l.Files {
			office = office || strings.HasSuffix(f.Name, c.ext)
			md = md || strings.HasSuffix(f.Name, ".md")
		}
		if !office || !md {
			t.Fatalf("%s: office=%v md=%v files=%v", c.ext, office, md, l.Files)
		}
	}
	// the workbook has a sheet per section and the injected text stays text
	folder, _ := svc.EnsureOwnedFolder(ctx, drive.RootProjects, "p-.xlsx", "Hazme un Excel con 10 productos")
	l, _ := svc.List(ctx, folder.Path)
	for _, f := range l.Files {
		if !strings.HasSuffix(f.Name, ".xlsx") {
			continue
		}
		rc, _, err := svc.Open(ctx, f, 0)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		x, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		got := strings.Join(x.GetSheetList(), ",")
		if got != "Summary,Findings,Recommendations,Metrics" {
			t.Fatalf("sheets = %s", got)
		}
		if fm, _ := x.GetCellFormula("Findings", "B3"); fm != "" {
			t.Fatalf("injected text became a formula %q", fm)
		}
	}
	// a request with no format keeps only the markdown
	sink.NodeChanged(ctx, projects.NodeEvent{ProjectID: "p-none", ProjectName: "Cierre", NodeID: "p-none:t1", Kind: projects.KindTask, Title: "Cuadre",
		AgentID: "analyst", State: projects.StateDone, Output: out})
	f2, _ := svc.EnsureOwnedFolder(ctx, drive.RootProjects, "p-none", "Cierre")
	l2, _ := svc.List(ctx, f2.Path)
	if len(l2.Files) != 1 || !strings.HasSuffix(l2.Files[0].Name, ".md") {
		t.Fatalf("files = %v", l2.Files)
	}
}
