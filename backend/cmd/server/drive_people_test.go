package main

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/drive"
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
