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
