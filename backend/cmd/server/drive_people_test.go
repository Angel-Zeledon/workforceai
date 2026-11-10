package main

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/offices"
)

func officePeopleFix(t *testing.T) officePeople {
	t.Helper()
	ctx := context.Background()
	st := offices.NewMemStore()
	svc := offices.New(offices.Config{Store: st,
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-open", Name: "Abierta"}, {ID: "d-priv", Name: "Privada"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-ana": "d-open", "u-pri": "d-priv"}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-ana", Name: "Ana"}, {UserID: "u-pri", Name: "Pri"}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	if _, err := svc.Enable(ctx, "org1", boss, "d-open", offices.Open); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enable(ctx, "org1", boss, "d-priv", offices.Private); err != nil {
		t.Fatal(err)
	}
	return officePeople{svc: svc}
}

func TestOfficePeopleAccess(t *testing.T) {
	op := officePeopleFix(t)
	cases := []struct{ user, office, want string }{
		{"u-ana", "d-open", drive.AccessEdit},
		{"u-eve", "d-open", drive.AccessRead},
		{"u-boss", "d-open", drive.AccessRead},
		{"u-pri", "d-priv", drive.AccessEdit},
		{"u-eve", "d-priv", drive.AccessNone},
		{"u-boss", "d-priv", drive.AccessNone},
		{"u-eve", "d-unknown", drive.AccessNone},
		{"u-eve", offices.GeneralID, drive.AccessEdit},
		{"", offices.GeneralID, drive.AccessRead},
	}
	for _, c := range cases {
		got, err := op.OfficeAccess(context.Background(), "org1", c.user, c.office)
		if err != nil || got != c.want || (c.user == "" && got == drive.AccessEdit) {
			t.Errorf("%s in %s = %q %v, want %q", c.user, c.office, got, err, c.want)
		}
	}
}

func TestAgentOfficesOfModel(t *testing.T) {
	ctx := context.Background()
	svc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-acct", Name: "Contabilidad"}, {ID: "d-off", Name: "Sin oficina"}}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-acct": "d-acct", "a-off": "d-off"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "a-acct"}, {ID: "a-gen"}, {ID: "a-off"}}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}}, nil
		}})
	if _, err := svc.Enable(ctx, "org1", offices.Viewer{UserID: "u-boss", Admin: true}, "d-acct", offices.Private); err != nil {
		t.Fatal(err)
	}
	ao := agentOffices{svc: svc}
	for agent, want := range map[string]string{"a-acct": "d-acct", "a-gen": "", "a-off": "", "a-unknown": ""} {
		if got, err := ao.AgentOffice(ctx, "org1", agent); err != nil || got != want {
			t.Errorf("%s: office %q %v, want %q", agent, got, err, want)
		}
	}
	if got, err := (agentOffices{}).AgentOffice(ctx, "org1", "a-acct"); err != nil || got != "" {
		t.Errorf("without offices: %q %v", got, err)
	}
}

// officeDriveFix wires a drive to the real offices adapters (officePeople, agentOffices): Abierta (open, member u-ana,
// agent a-open), Privada (private, member u-pri, agent a-priv) and a folder Fantasma whose office does not exist.
func officeDriveFix(t *testing.T) (context.Context, *drive.Service, *drive.AgentFS) {
	t.Helper()
	ctx := application.WithOrg(context.Background(), "org1")
	osvc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-open", Name: "Abierta"}, {ID: "d-priv", Name: "Privada"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-ana": "d-open", "u-pri": "d-priv"}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-open": "d-open", "a-priv": "d-priv"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "a-open"}, {ID: "a-priv"}}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-ana", Name: "Ana"}, {UserID: "u-pri", Name: "Pri"}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	if _, err := osvc.Enable(ctx, "org1", boss, "d-open", offices.Open); err != nil {
		t.Fatal(err)
	}
	if _, err := osvc.Enable(ctx, "org1", boss, "d-priv", offices.Private); err != nil {
		t.Fatal(err)
	}
	dsvc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "org1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	dsvc.SetPeople(officePeople{svc: osvc})
	dsvc.SetAgentOffices(agentOffices{svc: osvc})
	for id, name := range map[string]string{"d-open": "Abierta", "d-priv": "Privada", "d-ghost": "Fantasma"} {
		if _, err := dsvc.EnsureOfficeFolder(ctx, id, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dsvc.Write(ctx, drive.Human("u-ana"), drive.WriteInput{Path: "/Offices/Abierta/a.md", Data: []byte("abierta")}); err != nil {
		t.Fatal(err)
	}
	return ctx, dsvc, &drive.AgentFS{Svc: dsvc}
}

// An agent of an open office working for a person who is not a member: it reads there (the person reads), and its
// writes ask a human (the person only reads). For a member it writes freely; on its own (no person) too.
func TestAgentOfOpenOfficeForNonMember(t *testing.T) {
	ctx, _, fs := officeDriveFix(t)
	eve := drive.WithRequester(ctx, "u-eve")
	if pl := fs.Plan(eve, "a-open", "read", map[string]any{"path": "/Offices/Abierta/a.md"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("eve read: %+v", pl)
	}
	if pl := fs.Plan(eve, "a-open", "write", map[string]any{"path": "/Offices/Abierta/b.md", "content": "x"}); pl.Deny != "" || !pl.Approval {
		t.Fatalf("eve write must ask a human: %+v", pl)
	}
	if r := fs.Run(eve, "a-open", "write", map[string]any{"path": "/Offices/Abierta/b.md", "content": "x"}, false); r.OK {
		t.Fatalf("eve write ran without approval: %+v", r)
	}
	for _, c := range []context.Context{drive.WithRequester(ctx, "u-ana"), ctx, drive.WithRequester(ctx, "scheduler")} {
		if pl := fs.Plan(c, "a-open", "write", map[string]any{"path": "/Offices/Abierta/b.md", "content": "x"}); pl.Deny != "" || pl.Approval {
			t.Fatalf("member or no person writes freely: %+v", pl)
		}
	}
	// eve has no access to the private office: its agent does nothing there for her
	if pl := fs.Plan(eve, "a-priv", "write", map[string]any{"path": "/Offices/Privada/b.md", "content": "x"}); pl.Deny != "person_no_access" {
		t.Fatalf("eve in Privada: %+v", pl)
	}
	// a token is nobody the drive can tell: no office at all
	if pl := fs.Plan(drive.WithRequester(ctx, "token:abc"), "a-open", "read", map[string]any{"path": "/Offices/Abierta/a.md"}); pl.Deny != "person_no_access" {
		t.Fatalf("token: %+v", pl)
	}
}

// file.activity of a path inside an office's folder goes only to that office's members; when they cannot be told the
// event names no file. Elsewhere it stays for the whole organization.
func TestFileActivityAudience(t *testing.T) {
	ctx, dsvc, _ := officeDriveFix(t)
	ev := func(p string) application.Action {
		return fileActivity(ctx, dsvc, "a-open", map[string]any{"agent_id": "a-open", "action": "write", "path": p})
	}
	if a := ev("/Shared/x.md"); len(a.Audience) != 0 || a.Payload.(map[string]any)["path"] != "/Shared/x.md" || a.Type != "file.activity" {
		t.Fatalf("shared: %+v", a)
	}
	if a := ev("/Offices/Abierta/x.md"); !slices.Equal(a.Audience, []string{"u-ana"}) || a.Payload.(map[string]any)["path"] != "/Offices/Abierta/x.md" {
		t.Fatalf("open office: %+v", a)
	}
	if a := ev("/Offices/Privada/x.md"); !slices.Equal(a.Audience, []string{"u-pri"}) {
		t.Fatalf("private office: %+v", a)
	}
	if a := ev("/offices/privada/x.md"); !slices.Equal(a.Audience, []string{"u-pri"}) {
		t.Fatalf("private office, other case: %+v", a)
	}
	if a := ev("/Offices/Fantasma/x.md"); len(a.Audience) != 0 {
		t.Fatalf("unknown office must not go to everyone with its path: %+v", a)
	} else if _, has := a.Payload.(map[string]any)["path"]; has {
		t.Fatalf("unknown office: the path is dropped: %+v", a)
	}
	if a := fileActivity(ctx, nil, "a-open", map[string]any{"path": "/Offices/Privada/x.md"}); a.Payload.(map[string]any)["path"] == nil {
		t.Fatalf("no drive: unchanged: %+v", a)
	}
}
