package api_test

import (
	"context"
	"slices"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/roles"
)

func accountingFolders() []string {
	p, _ := roles.ProfessionOf("accounting")
	return p.Folders("es")
}

// D1: a company department is born as a private office with its profession's folders.

func newDeptDriveEnv(t *testing.T) (*teamsEnv, *drive.Service) {
	t.Helper()
	var dr *drive.Service
	e := newTeamsEnv(t, func(d *api.Deps) {
		d.Drive = drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: d.Cfg.OrgID, Log: d.Log})
		dr = d.Drive
		withOffices(d)
	})
	return e, dr
}

func folderNames(t *testing.T, dr *drive.Service, org, path string) []string {
	t.Helper()
	l, err := dr.List(application.WithOrg(context.Background(), org), path)
	if err != nil {
		t.Fatalf("list %s: %v", path, err)
	}
	out := []string{}
	for _, f := range l.Folders {
		out = append(out, f.Name)
	}
	return out
}

type officesList struct {
	Offices []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
		FolderPath string `json:"folder_path"`
	} `json:"offices"`
}

func TestCompanyTeamIsPrivateOfficeWithTemplate(t *testing.T) {
	e, dr := newDeptDriveEnv(t)
	s := e.signup(auth.RegisterInput{Email: "ceo@empresa.com", Name: "Ceo", OrgName: "Empresa", Audience: auth.AudienceCompany, Locale: "es"})
	var out struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if c := e.call("POST", "/api/v1/teams", s.AccessToken, map[string]any{"profession": "accounting"}, &out); c != 201 {
		t.Fatalf("create team = %d", c)
	}
	var ol officesList
	if c := e.call("GET", "/api/v1/offices", s.AccessToken, nil, &ol); c != 200 {
		t.Fatalf("offices = %d", c)
	}
	found := false
	for _, o := range ol.Offices {
		if o.ID == out.Team.ID {
			found = true
			if o.Visibility != "private" || o.FolderPath != "/Offices/Contabilidad" {
				t.Fatalf("office = %+v", o)
			}
		}
	}
	if !found {
		t.Fatalf("no office for the new department: %+v", ol)
	}
	want := accountingFolders()
	got := folderNames(t, dr, s.OrgID, "/Offices/Contabilidad")
	if len(want) != 6 || !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("folders = %v, want %v", got, want)
	}
}

func TestTeamAudienceStaysPlainDepartment(t *testing.T) {
	e, dr := newDeptDriveEnv(t)
	s := e.signup(auth.RegisterInput{Email: "t@equipo.com", Name: "T", OrgName: "Equipo", Audience: auth.AudienceTeam, Locale: "es"})
	if c := e.call("POST", "/api/v1/teams", s.AccessToken, map[string]any{"profession": "accounting"}, nil); c != 201 {
		t.Fatalf("create team = %d", c)
	}
	var ol officesList
	e.call("GET", "/api/v1/offices", s.AccessToken, nil, &ol)
	for _, o := range ol.Offices {
		if o.FolderPath != "" {
			t.Fatalf("office with folder in a team org: %+v", o)
		}
	}
	if got := folderNames(t, dr, s.OrgID, "/Offices"); len(got) != 0 {
		t.Fatalf("/Offices = %v", got)
	}
}

func TestEnableOfficeCreatesGenericFolder(t *testing.T) {
	e, dr := newDeptDriveEnv(t)
	s := e.signup(auth.RegisterInput{Email: "o@equipo.com", Name: "O", OrgName: "Equipo", Audience: auth.AudienceTeam, Locale: "es"})
	var dep struct {
		ID string `json:"id"`
	}
	if c := e.call("POST", "/api/v1/org/departments", s.AccessToken, map[string]any{"name": "Ventas"}, &dep); c != 201 {
		t.Fatalf("dept = %d", c)
	}
	var o struct {
		FolderPath string `json:"folder_path"`
	}
	if c := e.call("POST", "/api/v1/offices", s.AccessToken, map[string]any{"department_id": dep.ID, "visibility": "open"}, &o); c != 201 {
		t.Fatalf("enable = %d", c)
	}
	if o.FolderPath != "/Offices/Ventas" {
		t.Fatalf("folder_path = %q", o.FolderPath)
	}
	got := folderNames(t, dr, s.OrgID, "/Offices/Ventas")
	if !slices.Equal(slices.Sorted(slices.Values(got)), []string{"Documentos", "Políticas"}) {
		t.Fatalf("folders = %v", got)
	}
}
