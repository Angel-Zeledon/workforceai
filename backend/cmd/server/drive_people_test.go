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
