package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/roles"
)

// Each role finds its work kit on its desk the first time; nothing is duplicated
// and a desk that already has files is never touched.
func TestDeskWorkKit(t *testing.T) {
	ctx := context.Background()
	mem := memory.New()
	if err := mem.Seed(ctx, domain.SeedOrg(25), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	org := domain.SeedOrg(25).ID
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ws := driveWorkspace{drive: svc, store: mem}

	got := ws.Workspace(ctx, org, "sales", "")
	if got.Desk != "/Agents/sales" {
		t.Fatalf("desk = %q", got.Desk)
	}
	names := func(path string) []string {
		l, err := svc.List(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range l.Files {
			out = append(out, f.Name)
		}
		return out
	}
	kit := strings.Join(names("/Agents/sales"), ",")
	for _, want := range []string{"como-trabajo.md", "pipeline.csv", "plantilla-propuesta.md"} {
		if !strings.Contains(kit, want) {
			t.Fatalf("sales kit %q misses %s", kit, want)
		}
	}
	f, _ := svc.FileByPath(ctx, "/Agents/sales/como-trabajo.md")
	if tx, _ := svc.ReadText(ctx, f, 0, 0, 0); !strings.Contains(tx.Text, "Mis responsabilidades") || f.UpdatedBy != drive.System() {
		t.Fatalf("how-I-work note: %q by %+v", tx.Text, f.UpdatedBy)
	}

	// a second resolve adds nothing (no new versions)
	ws.Workspace(ctx, org, "sales", "")
	for _, n := range names("/Agents/sales") {
		ff, _ := svc.FileByPath(ctx, "/Agents/sales/"+n)
		if ff.CurrentVersion != 1 {
			t.Fatalf("%s was rewritten (v%d)", n, ff.CurrentVersion)
		}
	}

	// a desk that already has work is never seeded
	if _, err := svc.Write(ctx, drive.AgentP("legal"), drive.WriteInput{Path: "/Agents/legal/mi-nota.md", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	ws.Workspace(ctx, org, "legal", "")
	if n := names("/Agents/legal"); len(n) != 1 {
		t.Fatalf("a desk with files must not be seeded: %v", n)
	}

	// English organizations get the English kit
	ws.locale = func(context.Context) string { return "en" }
	ws.Workspace(ctx, org, "accounting", "")
	if kit := strings.Join(names("/Agents/accounting"), ","); !strings.Contains(kit, "how-i-work.md") || !strings.Contains(kit, "checklist-cierre-mensual.md") {
		t.Fatalf("accounting kit (en): %s", kit)
	}
	ff, _ := svc.FileByPath(ctx, "/Agents/accounting/checklist-cierre-mensual.md")
	if tx, _ := svc.ReadText(ctx, ff, 0, 0, 0); !strings.Contains(tx.Text, "Month-end close") {
		t.Fatalf("english content expected: %q", tx.Text)
	}
}
