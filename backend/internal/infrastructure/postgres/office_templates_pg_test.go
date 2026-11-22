package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	ot "aiworkforce/backend/internal/officetemplates"
)

// Integration test of migration 826 (TEST_DATABASE_URL, see store_rls_test.go): an imported office template round
// trips, the same content is kept once, and another organization neither reads nor sees it.
func TestOfficeTemplatesPostgres(t *testing.T) {
	env := testStore(t)
	st := &OfficeTemplateStore{S: env.Store}
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	tpl := ot.Template{Format: ot.Format, FormatVersion: 1, Kind: ot.KindDepartment, Name: "Contabilidad",
		Departments: []ot.Department{{Key: "d1", Name: "Contabilidad"}}, Agents: []ot.Agent{{Department: "d1", Role: "accounting"}},
		Skills: []ot.Skill{}, Workflows: []ot.Workflow{}, Schedules: []ot.Schedule{}, Goals: []ot.Goal{}}
	lib := &ot.Library{Store: st}
	s, created, err := lib.Save(ctx, "org-a", "u1", tpl)
	if err != nil || !created {
		t.Fatalf("save = %v %v", created, err)
	}
	again, created, err := lib.Save(ctx, "org-a", "u1", tpl)
	if err != nil || created || again.ID != s.ID {
		t.Fatalf("second save = %+v %v %v", again, created, err)
	}
	got, err := st.GetTemplate(ctx, "org-a", s.ID)
	if err != nil || got.Template.Agents[0].Role != "accounting" || got.Fingerprint != s.Fingerprint || !got.CreatedAt.Round(time.Second).Equal(s.CreatedAt.Round(time.Second)) {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if _, err := st.GetTemplate(ctx, "org-b", s.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads org-a's template: %v", err)
	}
	err = env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM office_templates WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d org-a templates", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTemplate(ctx, "org-b", s.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b deletes org-a's template: %v", err)
	}
	if err := st.DeleteTemplate(ctx, "org-a", s.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListTemplates(ctx, "org-a"); len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

func TestMigration826DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	down, err := os.ReadFile("../../../migrations/down/826_office_templates_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := migrationsFS.ReadFile("migrations/826_office_templates.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('office_templates')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	var forced bool
	_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='office_templates'`).Scan(&forced)
	if !forced {
		t.Error("row level security is not forced after the up migration")
	}
}
