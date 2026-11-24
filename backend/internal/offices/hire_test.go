package offices

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func TestCheckHire(t *testing.T) {
	f := multiFix(t, false) // the flag does not matter to hiring
	ctx := context.Background()
	if o, err := f.svc.CheckHire(ctx, org, admin, "", AccessInput{}); err != nil || o != GeneralID {
		t.Fatalf("general = %q %v", o, err)
	}
	if o, err := f.svc.CheckHire(ctx, org, ana, "d-legal", AccessInput{Visibility: VisOffice}); err != nil || o != "d-legal" {
		t.Fatalf("lead hires for her office: %q %v", o, err)
	}
	_, err := f.svc.CheckHire(ctx, org, ana, "d-sales", AccessInput{}) // leads Legal, not Ventas
	wantForbidden(t, err)
	_, err = f.svc.CheckHire(ctx, org, luis, "d-legal", AccessInput{}) // a member
	wantForbidden(t, err)
	_, err = f.svc.CheckHire(ctx, org, ana, GeneralID, AccessInput{}) // General is the admins'
	wantForbidden(t, err)
	if _, err := f.svc.CheckHire(ctx, org, admin, "d-nowhere", AccessInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown office: %v", err)
	}
	if _, err := f.svc.CheckHire(ctx, org, admin, "d-legal", AccessInput{Visibility: "x"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad visibility: %v", err)
	}
	if _, err := f.svc.CheckHire(ctx, org, admin, "d-legal", AccessInput{Shares: []Share{{UserID: "u-ghost"}}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("share with a stranger: %v", err)
	}
	// a private office is not revealed to an admin who is not in it
	if _, err := f.svc.SetVisibility(ctx, org, admin, "d-sales", Private); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CheckHire(ctx, org, admin, "d-sales", AccessInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("private office for an admin who is not in it: %v", err)
	}
	// ForgetAgent leaves no trace
	f.store.PutAgentOffice(ctx, org, AgentOffice{AgentID: "a-new", OfficeID: "d-legal"})
	if err := f.svc.ForgetAgent(ctx, org, "a-new"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := f.store.ListAgentOffices(ctx, org); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}
