// Package officestest is the contract every offices.Store must honour (memory and Postgres).
package officestest

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// Run checks a store with two organizations (orgA and orgB must exist when the store needs them).
func Run(t *testing.T, st offices.Store, orgA, orgB string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.PutOffice(ctx, orgA, offices.OfficeRow{DepartmentID: "d1", Visibility: offices.Open, CreatedBy: "u1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutOffice(ctx, orgA, offices.OfficeRow{DepartmentID: "d1", Visibility: offices.Private, CreatedAt: now, UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListOffices(ctx, orgA)
	if err != nil || len(rows) != 1 || rows[0].Visibility != offices.Private {
		t.Fatalf("offices = %+v %v", rows, err)
	}
	if rows, _ := st.ListOffices(ctx, orgB); len(rows) != 0 {
		t.Fatalf("other org sees offices: %+v", rows)
	}
	for _, u := range []string{"u1", "u2"} {
		if err := st.PutMember(ctx, orgA, offices.MemberRow{OfficeID: "d1", UserID: u, Role: offices.RoleMember, AddedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutMember(ctx, orgA, offices.MemberRow{OfficeID: "d1", UserID: "u1", Role: offices.RoleLead, AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	ms, _ := st.ListMembers(ctx, orgA)
	if len(ms) != 2 || ms[0].UserID != "u1" || ms[0].Role != offices.RoleLead {
		t.Fatalf("members = %+v", ms)
	}
	if err := st.DeleteMember(ctx, orgA, "d1", "u9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing member: %v", err)
	}
	a := offices.AgentAccess{AgentID: "a1", OwnerKind: offices.OwnerPerson, OwnerID: "u1", HomeOffice: "d1", Visibility: offices.VisPeople,
		Shares: []offices.Share{{UserID: "u2", Level: offices.ShareEdit}}, UpdatedBy: "u1", UpdatedAt: now}
	if err := st.PutAgentAccess(ctx, orgA, a); err != nil {
		t.Fatal(err)
	}
	as, _ := st.ListAgentAccess(ctx, orgA)
	if len(as) != 1 || as[0].HomeOffice != "d1" || len(as[0].Shares) != 1 || as[0].Shares[0].Level != offices.ShareEdit {
		t.Fatalf("access = %+v", as)
	}
	if as, _ := st.ListAgentAccess(ctx, orgB); len(as) != 0 {
		t.Fatal("other org sees agent access")
	}
	if err := st.DeleteAgentAccess(ctx, orgA, "a1"); err != nil {
		t.Fatal(err)
	}
	if as, _ := st.ListAgentAccess(ctx, orgA); len(as) != 0 {
		t.Fatal("access not deleted")
	}
	n1 := offices.Note{ID: "n1", Scope: offices.ScopeOffice, OwnerRef: "d1", Text: "Usamos la plantilla A", CreatedBy: "u1", CreatedAt: now}
	n2 := offices.Note{ID: "n2", Scope: offices.ScopeCompany, Text: "Usamos la plantilla A", PromotedFrom: "n1", CreatedBy: "u1", CreatedAt: now.Add(time.Second)}
	for _, n := range []offices.Note{n1, n2} {
		if err := st.PutNote(ctx, orgA, n); err != nil {
			t.Fatal(err)
		}
	}
	if ns, _ := st.ListNotes(ctx, orgA, "", ""); len(ns) != 2 || ns[0].ID != "n2" || ns[0].PromotedFrom != "n1" {
		t.Fatalf("notes = %+v", ns)
	}
	if ns, _ := st.ListNotes(ctx, orgA, offices.ScopeOffice, "d1"); len(ns) != 1 {
		t.Fatalf("office notes = %+v", ns)
	}
	if err := st.DeleteNote(ctx, orgA, "n1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteNote(ctx, orgA, "n1", "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if ns, _ := st.ListNotes(ctx, orgA, offices.ScopeOffice, ""); len(ns) != 0 {
		t.Fatal("deleted note still listed")
	}
	if ns, _ := st.ListNotes(ctx, orgB, "", ""); len(ns) != 0 {
		t.Fatal("other org sees notes")
	}
	if err := st.DeleteOffice(ctx, orgA, "d1"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := st.ListMembers(ctx, orgA); len(ms) != 0 {
		t.Fatal("members survive their office")
	}
	if err := st.DeleteOffice(ctx, orgA, "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing office: %v", err)
	}
}
