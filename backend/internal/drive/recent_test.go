package drive_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newRecentEnv(t *testing.T) (*drive.Service, *drive.AgentFS, context.Context, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1", Now: c.now})
	ctx := application.WithOrg(context.Background(), "o1")
	return svc, &drive.AgentFS{Svc: svc}, ctx, c
}

func put(t *testing.T, svc *drive.Service, ctx context.Context, by drive.Principal, p string) {
	t.Helper()
	if _, err := svc.Write(ctx, by, drive.WriteInput{Path: p, Data: []byte("contenido " + p)}); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
}

func TestRecentFilesOfThePersonWhoAsked(t *testing.T) {
	svc, fs, ctx, c := newRecentEnv(t)
	ana, luis := drive.Human("ana"), drive.Human("luis")
	c.t = c.t.Add(-30 * 24 * time.Hour)
	put(t, svc, ctx, ana, "/Shared/Facturas/Septiembre/vieja.md") // outside the 14 days
	c.t = c.t.Add(30 * 24 * time.Hour)
	for _, n := range []string{"f1.md", "f2.md", "f3.md"} {
		put(t, svc, ctx, ana, "/Shared/Facturas/Octubre/"+n)
	}
	put(t, svc, ctx, ana, "/Shared/Facturas/Octubre/resumen.csv")
	put(t, svc, ctx, luis, "/Shared/Facturas/Octubre/de-luis.md") // someone else
	put(t, svc, ctx, ana, "/Clients/Acme/contrato.md")            // a folder the agent cannot see
	put(t, svc, ctx, ana, "/Shared/RRHH/nomina.md")               // Ninguno for this agent
	if _, err := svc.SetAgentAccess(ctx, drive.Human("admin"), "sales", "/Shared/RRHH", drive.AccessNone); err != nil {
		t.Fatal(err)
	}
	ctxAna := drive.WithRequester(ctx, "ana")

	r := fs.Run(ctxAna, "sales", drive.ActRecent, map[string]any{"kind": "text"}, false)
	if !r.OK || r.Data["count"] != 3 || !strings.Contains(r.Text, "[folder] /Shared/Facturas/Octubre | 3 file(s)") {
		t.Fatalf("recent pdf-like texts of ana: %+v", r)
	}
	for _, leak := range []string{"vieja", "de-luis", "contrato", "nomina", "resumen.csv"} {
		if strings.Contains(r.Text, leak) {
			t.Fatalf("%s must not be listed: %s", leak, r.Text)
		}
	}
	if !strings.Contains(r.Text, "no access: 1 recent file(s) of this person in /Clients/Acme") ||
		!strings.Contains(r.Text, "no access: 1 recent file(s) of this person in /Shared/RRHH") || !strings.Contains(r.Summary, "2 folder(s) without access") {
		t.Fatalf("folders without access must be said (count only): %s", r.Text)
	}
	// any kind, 40 days: the old one too; "anyone" adds Luis's file and drops the no-access report
	r = fs.Run(ctxAna, "sales", drive.ActRecent, map[string]any{"days": 40}, false)
	if r.Data["count"] != 5 || !strings.Contains(r.Text, "vieja.md") {
		t.Fatalf("40 days: %+v", r)
	}
	r = fs.Run(ctxAna, "sales", drive.ActRecent, map[string]any{"by": "anyone", "path": "/Shared/Facturas"}, false)
	if r.Data["count"] != 5 || !strings.Contains(r.Text, "de-luis.md") || strings.Contains(r.Text, "no access") {
		t.Fatalf("anyone in a folder: %+v", r)
	}
	// a folder the agent cannot see is refused with the offer to give access
	pl := fs.Plan(ctxAna, "sales", drive.ActRecent, map[string]any{"path": "/Clients/Acme"})
	if pl.Deny != "not_granted" || !strings.Contains(pl.DenyMsg, "give you access") {
		t.Fatalf("plan: %+v", pl)
	}
	if pl := fs.Plan(ctxAna, "sales", drive.ActRecent, map[string]any{"kind": "???"}); pl.Deny != "invalid_args" {
		t.Fatalf("bad kind: %+v", pl)
	}
	// unknown person: everybody's files, said plainly
	r = fs.Run(ctx, "sales", drive.ActRecent, map[string]any{}, false)
	if !strings.Contains(r.Text, "the person who asked is not known") || !strings.Contains(r.Text, "de-luis.md") {
		t.Fatalf("unknown person: %s", r.Text)
	}
	// a move counts as touching the file
	f, _ := svc.FileByPath(ctx, "/Shared/Facturas/Octubre/de-luis.md")
	c.t = c.t.Add(time.Hour)
	if _, err := svc.MoveFile(ctx, ana, f.ID, "/Shared/Facturas/Octubre/movido.md"); err != nil {
		t.Fatal(err)
	}
	r = fs.Run(ctxAna, "sales", drive.ActRecent, map[string]any{"kind": "text"}, false)
	if r.Data["count"] != 4 || !strings.Contains(r.Text, "movido.md") {
		t.Fatalf("moved by ana: %+v", r)
	}
}

func TestRecentThroughTheGatewayUsesTheRequester(t *testing.T) {
	svc, fs, ctx, _ := newRecentEnv(t)
	put(t, svc, ctx, drive.Human("ana"), "/Shared/Facturas/a.md")
	put(t, svc, ctx, drive.Human("luis"), "/Shared/Facturas/b.md")
	tool := &drive.GatewayTool{FS: fs}
	res := tool.Run(ctx, application.GatewayCall{Org: "o1", AgentID: "sales", OnBehalfOf: "ana", Tool: "fs", Action: "recent", Args: map[string]any{}}, false)
	if res.Status != "succeeded" || len(res.Blocks) != 1 || !strings.Contains(res.Blocks[0], "a.md") || strings.Contains(res.Blocks[0], "b.md") {
		t.Fatalf("gateway: %+v", res)
	}
	if side, known := tool.SideEffect("recent"); side || !known {
		t.Fatal("recent is a read")
	}
}
