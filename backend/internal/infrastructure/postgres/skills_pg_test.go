package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/skills"
)

// Integration test of migration 510 (see store_rls_test.go: TEST_DATABASE_URL).

func skillSvc(env *testEnv) *skills.Service {
	return skills.New(skills.Config{Store: &SkillStore{S: env.Store}, Drive: driveSvc(env), OrgID: "org-a",
		AgentRole: func(_ context.Context, _, id string) (string, error) {
			if id == "sales" {
				return "sales", nil
			}
			return "", domain.ErrNotFound
		}})
}

func TestSkillStoreLifecycleRLSAndImmutability(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	svc := skillSvc(env)
	ctxA := application.WithOrg(ctx, "org-a")
	adm := skills.Actor{ID: "boss", Admin: true}
	mem := skills.Actor{ID: "maria"}

	d, err := svc.Create(ctxA, mem, skills.Input{Name: "pg-skill", Description: "persisted", Instructions: "# Body\n", Files: []skills.File{{Path: "templates/a.md", Content: "a\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctxA, mem, skills.Input{Name: "PG-SKILL", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("name is case-insensitively unique")
	}
	if _, err := svc.Submit(ctxA, mem, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Review(ctxA, adm, d.ID, true, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctxA, adm, "sales", d.ID); err != nil {
		t.Fatal(err)
	}
	edit := skills.Input{Name: "pg-skill", Description: "persisted v2", Instructions: "# Body 2\n"}
	d2, changed, err := svc.Update(ctxA, mem, d.ID, edit)
	if err != nil || !changed || d2.Status != skills.StatusInReview || d2.ActiveVersion != 1 || d2.CurrentVersion != 2 {
		t.Fatalf("update: %v %+v", err, d2.Skill)
	}
	refs := svc.SkillRefs(ctxA, "org-a", "sales")
	if len(refs) != 1 || refs[0].Description != "persisted" {
		t.Fatalf("refs: %+v", refs)
	}
	l, err := svc.Load(ctxA, "org-a", skills.LoadCall{AgentID: "sales", Name: "pg-skill"})
	if err != nil || l.Version != 1 || !strings.Contains(l.Text, "Body") {
		t.Fatalf("load: %v %+v", err, l)
	}
	list, err := svc.List(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		if s.Name == "pg-skill" {
			found = true
			if s.Agents != 1 {
				t.Fatalf("agents count: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("not listed")
	}
	// export then import
	z, _, err := svc.Export(ctxA, adm, d.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctxA, mem, z, "pg-skill-copy"); err != nil {
		t.Fatal(err)
	}
	// delete and bring back
	if err := svc.Delete(ctxA, adm, d.ID); err != nil {
		t.Fatal(err)
	}
	again, err := svc.Create(ctxA, mem, skills.Input{Name: "pg-skill", Description: "again", Instructions: "# Again\n"})
	if err != nil || again.CurrentVersion != 3 || again.ID != d.ID {
		t.Fatalf("recreate: %v %+v", err, again.Skill)
	}

	// RLS: org-b sees and writes nothing of org-a
	for _, tbl := range []string{"skills", "skill_versions", "skill_assignments"} {
		var n int
		err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error { return tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n) })
		if err != nil || n != 0 {
			t.Fatalf("%s: org-b sees %d rows (%v)", tbl, n, err)
		}
	}
	if _, err := (&SkillStore{S: env.Store}).GetSkill(ctx, "org-b", d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if err := env.exec(ctx, "org-b", `INSERT INTO skills (org_id, id, name, description, status, source, current_version, created_by) VALUES ('org-a','x','x','x','draft','created',1,'u')`); err == nil {
		t.Fatal("org-b wrote a skill of org-a (WITH CHECK)")
	}
	// versions are immutable; skills are never hard-deleted by the application role
	if err := env.exec(ctx, "org-a", `UPDATE skill_versions SET changelog='x'`); err == nil {
		t.Fatal("skill_versions must be immutable")
	}
	if err := env.exec(ctx, "org-a", `DELETE FROM skill_versions`); err == nil {
		t.Fatal("skill_versions must not be deletable")
	}
	if err := env.exec(ctx, "org-a", `DELETE FROM skills`); err == nil {
		t.Fatal("skills must not be deletable by app_user")
	}
	// down migration
	down, err := os.ReadFile("../../../migrations/down/510_skills_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.owner(t).Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
}
