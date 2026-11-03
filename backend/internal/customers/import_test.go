package customers_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/customers"
)

func importSvc(t *testing.T) (*customers.Service, context.Context) {
	t.Helper()
	ctx := application.WithOrg(context.Background(), org)
	svc := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org})
	s := func(v string) *string { return &v }
	if _, err := svc.Create(ctx, "ana", customers.Input{Name: s("ACME S.A."), Email: s("Compras@Acme.com"), TaxID: s("3-101-1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "ana", customers.Input{Name: s("José Pérez")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "ana", customers.Input{Name: s("Beta Ltda"), TaxID: s("3-102-9")}); err != nil {
		t.Fatal(err)
	}
	return svc, ctx
}

func TestImportMatchByEmailThenTaxThenName(t *testing.T) {
	svc, ctx := importSvc(t)
	rows := []customers.ImportRow{
		{Name: "Cualquiera", Company: "Otra empresa", Email: "compras@ACME.com "}, // e-mail wins over the name
		{Company: "Beta", TaxID: "3-102-9"},                                         // tax id
		{Name: "  jose   PEREZ "},                                                  // normalized name (accents, case, spaces)
		{Name: "Nueva Persona", Email: "nueva@x.com", Phone: "+506 8888"},          // new
	}
	got, err := svc.PreviewImport(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ action, by string }{{"existing", "email"}, {"existing", "tax_id"}, {"existing", "name"}, {"new", ""}}
	for i, w := range want {
		if got[i].Action != w.action || got[i].MatchedBy != w.by {
			t.Fatalf("row %d: %+v, want %s by %s", i, got[i], w.action, w.by)
		}
	}
	if got[0].CustomerName != "ACME S.A." || got[3].Name != "Nueva Persona" {
		t.Fatalf("names: %+v", got)
	}
}

func TestImportRowErrors(t *testing.T) {
	svc, ctx := importSvc(t)
	got, _ := svc.PreviewImport(ctx, []customers.ImportRow{
		{Email: "sin-nombre@x.com"},
		{Name: "Malo", Email: "no-es-correo"},
		{Name: "Repetida", Email: "rep@x.com"},
		{Name: "Repetida 2", Email: "REP@x.com"},
	})
	if got[0].Action != "error" || got[0].Reason != "name_required" || got[1].Reason != "invalid_email" {
		t.Fatalf("errors: %+v", got[:2])
	}
	if got[2].Action != "new" || got[3].Action != "error" || got[3].Reason != "duplicate_in_file" {
		t.Fatalf("duplicates: %+v", got[2:])
	}
}

func TestApplyImportCreatesAndRespectsChoices(t *testing.T) {
	svc, ctx := importSvc(t)
	rows := []customers.ImportRow{
		{Name: "Nueva Persona", Email: "nueva@x.com", Phone: "8888", Notes: "VIP"},
		{Name: "jose perez"},     // matches José Pérez...
		{Name: "No la quiero"},   // ...and this one is skipped by the person
	}
	pre, _ := svc.PreviewImport(ctx, rows)
	choices := map[int]string{1: customers.ChoiceNew, 2: customers.ChoiceSkip}
	res, err := svc.ApplyImport(ctx, "ana", rows, choices)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].CustomerID == "" || res[0].Action != "created" || res[1].Action != "created" || res[1].CustomerID == pre[1].CustomerID || res[2].Action != "skipped" {
		t.Fatalf("apply: %+v", res)
	}
	c, _ := svc.Get(ctx, res[0].CustomerID)
	if c.Email != "nueva@x.com" || c.Phone != "8888" || c.Notes != "VIP" {
		t.Fatalf("created: %+v", c)
	}
	// a person may also point a row at another existing customer
	list, _ := svc.List(ctx, false)
	res, err = svc.ApplyImport(ctx, "ana", []customers.ImportRow{{Name: "Alguien"}}, map[int]string{0: list[0].ID})
	if err != nil || res[0].Action != "existing" || res[0].CustomerID != list[0].ID {
		t.Fatalf("pointed row: %+v %v", res, err)
	}
}
