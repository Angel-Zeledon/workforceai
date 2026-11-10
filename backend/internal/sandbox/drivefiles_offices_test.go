package sandbox

import (
	"context"
	"fmt"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

type oneOffice map[string]string

func (o oneOffice) AgentOffice(_ context.Context, _, agentID string) (string, error) {
	return o[agentID], nil
}

type noOffice struct{}

func (noOffice) OfficeAccess(context.Context, string, string, string) (string, error) {
	return drive.AccessNone, nil
}

// An input the agent may not read (another office, or an office the person cannot see) is exactly "not found":
// the request never reveals that the file exists.
func TestInputsOutsideTheAgentZonesAreNotFound(t *testing.T) {
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org})
	ctx := application.WithOrg(context.Background(), org)
	for id, name := range map[string]string{"dep_acct": "Contabilidad", "dep_legal": "Legal"} {
		if _, err := dr.EnsureOfficeFolder(ctx, id, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	var legalID string
	for _, p := range []string{"/Offices/Legal/x.md", "/Offices/Contabilidad/y.md"} {
		w, err := dr.Write(ctx, drive.Human("u"), drive.WriteInput{Path: p, Data: []byte("1,2\n")})
		if err != nil {
			t.Fatal(err)
		}
		if p == "/Offices/Legal/x.md" {
			legalID = w.File.ID
		}
	}
	e := newEnv(t, true)
	df := &DriveFiles{Drive: dr, FS: &drive.AgentFS{Svc: dr, Offices: oneOffice{"acct": "dep_acct"}}}
	e.svc.Files, e.svc.Sink = df, df
	prep := func(person, ref string) error {
		_, err := e.svc.Prepare(context.Background(), application.CodeRunInput{Org: org, AgentID: "acct", TaskID: "t1", ApprovalID: "ap-" + ref + person,
			OnBehalfOf: person, Args: args("print(1)", ref)})
		return err
	}
	for _, ref := range []string{"/Offices/Legal/x.md", legalID} {
		err := prep("", ref)
		want := fmt.Sprintf("input file %q was not found", ref)
		if CodeOf(err) != CodeInvalid || err == nil || err.Error() != invalid("input file %q was not found", ref).Error() {
			t.Fatalf("%s: %v (want %s)", ref, err, want)
		}
	}
	if err := prep("", "/Offices/Legal/nope.md"); err == nil || err.Error() != invalid("input file %q was not found", "/Offices/Legal/nope.md").Error() {
		t.Fatalf("a missing file: %v", err)
	}
	if err := prep("", "/Offices/Contabilidad/y.md"); err != nil {
		t.Fatalf("its own office is an input: %v", err)
	}
	dr.SetPeople(noOffice{})
	if err := prep("beto", "/Offices/Contabilidad/y.md"); err == nil || err.Error() != invalid("input file %q was not found", "/Offices/Contabilidad/y.md").Error() {
		t.Fatalf("beto cannot see Contabilidad: %v", err)
	}
	// a token is neither a person nor the platform: no office at all, not even the agent's own (fails closed)
	if err := prep("token:abc", "/Offices/Contabilidad/y.md"); err == nil || err.Error() != invalid("input file %q was not found", "/Offices/Contabilidad/y.md").Error() {
		t.Fatalf("a token gets no office: %v", err)
	}
	// the platform itself (a scheduled run) keeps the agent's office
	if err := prep("scheduler", "/Offices/Contabilidad/y.md"); err != nil {
		t.Fatalf("a scheduled run keeps its office: %v", err)
	}
}
