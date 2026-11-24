package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/offices"
)

// isolationFix: agent a-x has its base in Legal (private) and also works for Ventas (open); a-leg is Legal only.
// Plan docs/superpowers/plans/2026-11-23-aislamiento-oficinas.md.
func isolationFix(t *testing.T) (context.Context, *offices.Service, *drive.Service, *drive.AgentFS) {
	t.Helper()
	ctx := application.WithOrg(context.Background(), "org1")
	svc := offices.New(offices.Config{Store: offices.NewMemStore(), MultiOffice: true,
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-legal", Name: "Legal"}, {ID: "d-ven", Name: "Ventas"}}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-x": "d-legal", "a-leg": "d-legal"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "a-x"}, {ID: "a-leg"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-lea": "d-legal", "u-val": "d-ven"}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-lea", Name: "Lea"}, {UserID: "u-val", Name: "Val"}}, nil
		}})
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	if _, err := svc.Enable(ctx, "org1", boss, "d-legal", offices.Private); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enable(ctx, "org1", boss, "d-ven", offices.Open); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrantOffice(ctx, "org1", boss, "a-x", "d-ven"); err != nil { // an admin offers and accepts at once
		t.Fatal(err)
	}
	dsvc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "org1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	dsvc.SetPeople(officePeople{svc: svc})
	dsvc.SetAgentOffices(agentOffices{svc: svc})
	for id, name := range map[string]string{"d-legal": "Legal", "d-ven": "Ventas"} {
		if _, err := dsvc.EnsureOfficeFolder(ctx, id, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{"/Offices/Legal/s.md": "LEGALFILESECRET", "/Offices/Ventas/s.md": "VENTASFILESECRET"} {
		if _, err := dsvc.Write(ctx, drive.Human("u-boss"), drive.WriteInput{Path: p, Data: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, svc, dsvc, &drive.AgentFS{Svc: dsvc}
}

// Files (fs.*): the same agent reads the files of the office the request comes from and not the other's, also when the
// instruction tells it to open the other office's path.
func TestIsolation_Files(t *testing.T) {
	ctx, _, _, fs := isolationFix(t)
	read := func(office, agent, path string) string {
		c := application.WithRequestOffice(ctx, office)
		pl := fs.Plan(c, agent, "read", map[string]any{"path": path})
		if pl.Deny != "" {
			return "denied:" + pl.Deny
		}
		r := fs.Run(c, agent, "read", map[string]any{"path": path}, false)
		return r.Summary + " " + r.Text
	}
	cases := []struct {
		office, path string
		allowed      bool
	}{
		{"d-ven", "/Offices/Ventas/s.md", true},
		{"d-ven", "/Offices/Legal/s.md", false}, // a malicious instruction asking for the base office's file
		{"d-legal", "/Offices/Legal/s.md", true},
		{"d-legal", "/Offices/Ventas/s.md", false},
		{"", "/Offices/Legal/s.md", true},  // unknown office: the base
		{"", "/Offices/Ventas/s.md", false}, // and never another
	}
	for _, c := range cases {
		out := read(c.office, "a-x", c.path)
		leaked := strings.Contains(out, "FILESECRET")
		if c.allowed && !leaked || !c.allowed && leaked {
			t.Errorf("office %q reading %s: %q (allowed=%v)", c.office, c.path, out, c.allowed)
		}
	}
	// the agent whose base is Legal only never gets Ventas' files, whatever the request says
	if out := read("d-ven", "a-leg", "/Offices/Ventas/s.md"); strings.Contains(out, "FILESECRET") {
		t.Errorf("a Legal-only agent read Ventas: %q", out)
	}
}

// Team notes: an agent recalls the notes of the office the request comes from, not the other's.
func TestIsolation_TeamNotes(t *testing.T) {
	ctx, svc, _, _ := isolationFix(t)
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	for office, text := range map[string]string{"d-legal": "LEGALNOTESECRET clausula penal", "d-ven": "VENTASNOTESECRET clausula penal"} {
		if _, err := svc.AddNote(ctx, "org1", boss, offices.ScopeOffice, office, text); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ office, mine, other string }{
		{"d-ven", "VENTASNOTE", "LEGALNOTE"}, {"d-legal", "LEGALNOTE", "VENTASNOTE"}, {"", "LEGALNOTE", "VENTASNOTE"},
	} {
		var all string
		for _, n := range svc.RecallNotesFor(ctx, "org1", "a-x", c.office, "", "clausula penal", 10) {
			all += n.Text + "\n"
		}
		if !strings.Contains(all, c.mine) || strings.Contains(all, c.other) {
			t.Errorf("office %q recalled %q", c.office, all)
		}
	}
}

// Taking the agent out of Ventas ends it at once, and the removal runs the hook that blocks the work not started.
func TestIsolation_RemovedAgentLosesTheOffice(t *testing.T) {
	ctx, svc, _, _ := isolationFix(t)
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	var hooked string
	svc.SetOnOfficeRemoved(func(_ context.Context, _, agent, office string) { hooked = agent + "@" + office })
	ao := agentOffices{svc: svc}
	vctx := application.WithRequestOffice(ctx, "d-ven")
	if off, _ := ao.AgentOffice(vctx, "org1", "a-x"); off != "d-ven" {
		t.Fatalf("working for Ventas: %q", off)
	}
	if _, err := svc.RemoveOffice(ctx, "org1", boss, "a-x", "d-ven"); err != nil {
		t.Fatal(err)
	}
	if off, _ := ao.AgentOffice(vctx, "org1", "a-x"); off != "d-legal" { // back to the base, never another
		t.Fatalf("after removal: %q", off)
	}
	if ok, _ := ao.WorksFor(ctx, "org1", "a-x", "d-ven"); ok || hooked != "a-x@d-ven" {
		t.Fatalf("works=%v hook=%q", ok, hooked)
	}
}
