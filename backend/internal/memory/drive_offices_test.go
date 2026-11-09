package memory_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/memory"
)

type agentOffice map[string]string

func (a agentOffice) AgentOffice(_ context.Context, _, agentID string) (string, error) {
	return a[agentID], nil
}

type officeLevels map[string]string

func (o officeLevels) OfficeAccess(_ context.Context, _, _, officeID string) (string, error) {
	if l, ok := o[officeID]; ok {
		return l, nil
	}
	return drive.AccessNone, nil
}

// The long-term memory recalls drive files with the agent's office scope and never beyond the person it works for.
func TestDriveFilesRespectOfficesAndPerson(t *testing.T) {
	drv := newDrive()
	ctx := application.WithOrg(context.Background(), "org-a")
	for id, name := range map[string]string{"dep_acct": "Contabilidad", "dep_legal": "Legal"} {
		if _, err := drv.EnsureOfficeFolder(ctx, id, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		"/Offices/Contabilidad/balance.md": "conciliacion bancaria del trimestre",
		"/Offices/Legal/demanda.md":        "conciliacion judicial del caso",
	} {
		if _, err := drv.Write(ctx, drive.Human("u"), drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
			t.Fatal(err)
		}
	}
	files := &memory.DriveFiles{FS: &drive.AgentFS{Svc: drv, Offices: agentOffice{"acct": "dep_acct"}}}
	paths := func(ctx context.Context) map[string]bool {
		t.Helper()
		items, err := files.Files(ctx, "org-a", "acct", []string{"conciliacion"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, it := range items {
			out[it.Path] = true
		}
		return out
	}
	if got := paths(ctx); !got["/Offices/Contabilidad/balance.md"] || got["/Offices/Legal/demanda.md"] {
		t.Fatalf("own office only: %v", got)
	}
	drv.SetPeople(officeLevels{"dep_acct": drive.AccessNone})
	if got := paths(drive.WithRequester(ctx, "beto")); len(got) != 0 {
		t.Fatalf("beto cannot see Contabilidad: the memory must not recall it for him: %v", got)
	}
	// the person who started the work (the actor) counts when the call carries no requester
	if got := paths(application.WithActor(ctx, "beto")); len(got) != 0 {
		t.Fatalf("actor beto: %v", got)
	}
}

// An actor that is not a person (an API token) does not limit the agent: it keeps its office in the memory.
func TestDriveFilesNonPersonActorKeepsOffice(t *testing.T) {
	drv := newDrive()
	ctx := application.WithOrg(context.Background(), "org-a")
	if _, err := drv.EnsureOfficeFolder(ctx, "dep_acct", "Contabilidad", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := drv.Write(ctx, drive.Human("u"), drive.WriteInput{Path: "/Offices/Contabilidad/balance.md", Data: []byte("conciliacion bancaria")}); err != nil {
		t.Fatal(err)
	}
	drv.SetPeople(officeLevels{})
	files := &memory.DriveFiles{FS: &drive.AgentFS{Svc: drv, Offices: agentOffice{"acct": "dep_acct"}}}
	items, err := files.Files(application.WithActor(ctx, "token:abc"), "org-a", "acct", []string{"conciliacion"}, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("token actor: %v %+v", err, items)
	}
}

type linkedFolder map[string]string

func (linkedFolder) ProjectCustomer(context.Context, string, string) (string, error) { return "", nil }
func (l linkedFolder) LinkedFolders(context.Context, string) (map[string]string, error) {
	return l, nil
}

// In a task for a customer whose folder lives inside another office, the memory recalls nothing from it.
func TestDriveFilesCustomerFolderInAnotherOffice(t *testing.T) {
	drv := newDrive()
	ctx := application.WithOrg(context.Background(), "org-a")
	for id, name := range map[string]string{"dep_acct": "Contabilidad", "dep_legal": "Legal"} {
		if _, err := drv.EnsureOfficeFolder(ctx, id, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	acme := "/Offices/Contabilidad/Clientes/ACME"
	if _, err := drv.Write(ctx, drive.Human("u"), drive.WriteInput{Path: acme + "/estado.md", Data: []byte("conciliacion de acme")}); err != nil {
		t.Fatal(err)
	}
	l, err := drv.List(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	fs := &drive.AgentFS{Svc: drv, Offices: agentOffice{"acct": "dep_acct", "legal": "dep_legal"}, Customers: linkedFolder{l.Folder.ID: "cus_acme"}}
	files := &memory.DriveFiles{FS: fs}
	cctx := application.WithCustomer(ctx, "cus_acme")
	if items, err := files.Files(cctx, "org-a", "legal", []string{"conciliacion"}, 10); err != nil || len(items) != 0 {
		t.Fatalf("legal recalls ACME inside Contabilidad: %v %+v", err, items)
	}
	if items, err := files.Files(cctx, "org-a", "acct", []string{"conciliacion"}, 10); err != nil || len(items) != 1 {
		t.Fatalf("acct recalls its customer folder: %v %+v", err, items)
	}
}
