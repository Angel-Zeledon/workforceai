package offices

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func (f *fixture) note(t *testing.T, v Viewer, scope, ref, text string) Note {
	t.Helper()
	n, err := f.svc.AddNote(context.Background(), org, v, scope, ref, text)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOfficeNoteVisibility(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", Private)
	n := f.note(t, luis, ScopeOffice, "d-legal", "Los contratos usan la plantilla de la socia")
	if n.Scope != ScopeOffice || n.OwnerRef != "d-legal" || n.CreatedBy != "u-luis" {
		t.Fatalf("note = %+v", n)
	}
	if _, err := f.svc.AddNote(ctx, org, marta, ScopeOffice, "d-legal", "hola"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outsider writes: %v", err)
	}
	if _, err := f.svc.Notes(ctx, org, marta, ScopeOffice, "d-legal"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outsider reads a private office's memory: %v", err)
	}
	l, err := f.svc.Notes(ctx, org, luis, ScopeOffice, "d-legal")
	if err != nil || len(l.Notes) != 1 || !l.CanWrite || l.CanPromote {
		t.Fatalf("member reads: %+v %v", l, err)
	}
	if _, err := f.svc.SetVisibility(ctx, org, admin, "d-legal", Open); err != nil {
		t.Fatal(err)
	}
	l, err = f.svc.Notes(ctx, org, marta, ScopeOffice, "d-legal")
	if err != nil || len(l.Notes) != 1 || l.CanWrite {
		t.Fatalf("open office: anybody reads, members write: %+v %v", l, err)
	}
}

func TestPersonalNotePrivate(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.note(t, marta, ScopePerson, "u-luis", "Prefiero respuestas cortas") // the ref is always the writer
	if l, _ := f.svc.Notes(ctx, org, marta, ScopePerson, ""); len(l.Notes) != 1 || l.Notes[0].OwnerRef != "u-marta" {
		t.Fatalf("marta's memory = %+v", l.Notes)
	}
	if l, _ := f.svc.Notes(ctx, org, admin, ScopePerson, "u-marta"); len(l.Notes) != 0 {
		t.Fatal("nobody else reads a personal memory, not even an admin")
	}
	if l, _ := f.svc.Notes(ctx, org, luis, ScopePerson, ""); len(l.Notes) != 0 {
		t.Fatal("luis sees marta's memory")
	}
}

func TestPromoteOnlyLead(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", "")
	n := f.note(t, luis, ScopeOffice, "d-legal", "Plazo de respuesta a clientes: 48 horas")
	if _, err := f.svc.Promote(ctx, org, luis, n.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member promotes: %v", err)
	}
	c, err := f.svc.Promote(ctx, org, ana, n.ID)
	if err != nil || c.Scope != ScopeCompany || c.PromotedFrom != n.ID || c.Text != n.Text {
		t.Fatalf("promote = %+v %v", c, err)
	}
	if !contains(f.audits, "memory.promoted") {
		t.Fatalf("audits = %v", f.audits)
	}
	if _, err := f.svc.Promote(ctx, org, ana, n.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("promote twice: %v", err)
	}
	if l, _ := f.svc.Notes(ctx, org, marta, ScopeCompany, ""); len(l.Notes) != 1 || l.CanWrite {
		t.Fatalf("company knowledge: %+v", l)
	}
	if _, err := f.svc.AddNote(ctx, org, ana, ScopeCompany, "", "directo"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a non-admin writes company knowledge directly: %v", err)
	}
	// deleting: its author, the responsible or an admin
	n2 := f.note(t, luis, ScopeOffice, "d-legal", "Otra nota")
	if err := f.svc.DeleteNote(ctx, org, marta, n2.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outsider deletes: %v", err)
	}
	if err := f.svc.DeleteNote(ctx, org, ana, n2.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNoteRejectsUnsafe(t *testing.T) {
	f := newFix(t)
	f.svc.cfg.CleanNote = func(s string) string {
		if strings.Contains(s, "sk-") {
			return ""
		}
		return strings.TrimSpace(s)
	}
	if _, err := f.svc.AddNote(context.Background(), org, marta, ScopePerson, "", "mi clave es sk-12345"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unsafe note: %v", err)
	}
	if _, err := f.svc.AddNote(context.Background(), org, marta, ScopePerson, "", "   "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty note: %v", err)
	}
}

func TestRecallScopes(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", "")
	f.note(t, luis, ScopeOffice, "d-legal", "Los contratos de alquiler usan la plantilla de la socia")
	f.note(t, marta, ScopeOffice, GeneralID, "Las propuestas de alquiler llevan tres opciones")
	f.note(t, marta, ScopePerson, "", "Prefiero respuestas cortas")
	f.note(t, luis, ScopePerson, "", "Firmo como Lic. Luis")
	got := func(agent, person string) string {
		var b []string
		for _, n := range f.svc.RecallNotes(ctx, org, agent, person, "revisar contrato de alquiler", 5) {
			b = append(b, n.Text)
		}
		return strings.Join(b, " | ")
	}
	// Elena (Legal) working for Luis: Legal's memory and Luis's own, never Marta's nor General's office notes
	if r := got("a-elena", "u-luis"); !strings.Contains(r, "plantilla de la socia") || !strings.Contains(r, "Lic. Luis") ||
		strings.Contains(r, "cortas") || strings.Contains(r, "tres opciones") {
		t.Fatalf("elena for luis = %q", r)
	}
	// Valeria (General) working for Marta
	if r := got("a-valeria", "u-marta"); !strings.Contains(r, "tres opciones") || !strings.Contains(r, "cortas") || strings.Contains(r, "socia") {
		t.Fatalf("valeria for marta = %q", r)
	}
	// promoted knowledge reaches every agent; a scheduled task (no person) gets no personal memory
	l, _ := f.svc.Notes(ctx, org, luis, ScopeOffice, "d-legal")
	if _, err := f.svc.Promote(ctx, org, ana, l.Notes[0].ID); err != nil {
		t.Fatal(err)
	}
	if r := got("a-valeria", ""); !strings.Contains(r, "socia") || strings.Contains(r, "cortas") {
		t.Fatalf("valeria for nobody = %q", r)
	}
}
