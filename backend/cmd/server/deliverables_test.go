package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/projects"
)

type countSink struct{ n int }

func (c *countSink) NodeChanged(context.Context, projects.NodeEvent) { c.n++ }

func TestDeliverablesBecomeProjectFiles(t *testing.T) {
	ctx := context.Background()
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "org1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	next := &countSink{}
	sink := deliverableSink{next: next, drive: svc, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ev := projects.NodeEvent{ProjectID: "p1", ProjectName: "Cierre de caja", NodeID: "t3", Kind: projects.KindTask, Title: "Conciliación cuenta A",
		AgentID: "accounting", State: projects.StateDone,
		Output: &domain.StructuredOutput{Summary: "Cuadra al centavo.", Findings: []string{"2 partidas en tránsito"}, Metrics: map[string]any{"diferencia": 0}}}

	sink.NodeChanged(ctx, ev)
	sink.NodeChanged(ctx, ev) // replay: same content, no new version
	if next.n != 2 {
		t.Fatalf("the next sink must always run, ran %d", next.n)
	}
	folder, err := svc.EnsureOwnedFolder(ctx, drive.RootProjects, "p1", "Cierre de caja")
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.FileByPath(ctx, folder.Path+"/t3-conciliación-cuenta-a.md")
	if err != nil {
		t.Fatalf("deliverable file: %v", err)
	}
	if f.UpdatedBy != drive.AgentP("accounting") {
		t.Fatalf("attributed to %+v", f.UpdatedBy)
	}
	if vs, _ := svc.Versions(ctx, f.ID); len(vs) != 1 {
		t.Fatalf("a replay must not add versions: %d", len(vs))
	}
	tx, err := svc.ReadText(ctx, f, 0, 0, 0)
	if err != nil || !strings.Contains(tx.Text, "Cuadra al centavo.") || !strings.Contains(tx.Text, "| diferencia | 0 |") {
		t.Fatalf("content: %v %q", err, tx.Text)
	}

	// Unfinished nodes, groups and outputs without an agent write nothing.
	for _, e := range []projects.NodeEvent{
		{ProjectID: "p1", NodeID: "t4", Kind: projects.KindTask, Title: "x", AgentID: "a", State: "running", Output: ev.Output},
		{ProjectID: "p1", NodeID: "g1", Kind: projects.KindGroup, Title: "x", AgentID: "a", State: projects.StateDone, Output: ev.Output},
		{ProjectID: "p1", NodeID: "t5", Kind: projects.KindTask, Title: "x", State: projects.StateDone, Output: ev.Output},
	} {
		sink.NodeChanged(ctx, e)
	}
	l, _ := svc.List(ctx, folder.Path)
	if len(l.Files) != 1 {
		t.Fatalf("only the finished task writes a file, got %d", len(l.Files))
	}
}
