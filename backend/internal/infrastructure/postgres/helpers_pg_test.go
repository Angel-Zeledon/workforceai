package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
)

// Integration test of migration 831 (TEST_DATABASE_URL, see store_rls_test.go): a group round trips with the material
// of its parts, one group per request, the spend is added atomically and never overwritten by an update, tasks keep
// their helper mark, and another organization neither reads nor changes the group.
func TestHelperGroupsPostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	now := time.Now().UTC().Truncate(time.Millisecond)
	g := domain.HelperGroup{ID: "hg1", RequestID: "rq1", OwnerAgentID: "analyst", Mode: domain.HelperModePosition, Count: 2, MaxCount: 20, Parallel: 3,
		Parts: []domain.HelperPart{
			{Index: 1, Label: "Contratos 1", Text: "contrato 1", Files: []domain.FileRef{{Path: "/Offices/Legal/a.pdf", Name: "a.pdf"}}},
			{Index: 2, Label: "Contratos 2", Text: "contrato 2"},
		},
		Estimate: domain.HelperEstimate{MinUSD: 0.01, MaxUSD: 0.2}, CapUSD: 0.2, Status: domain.HelperRunning, AcceptedBy: "alice", CreatedAt: now}
	if err := st.CreateHelperGroup(ctx, "org-a", g); err != nil {
		t.Fatal(err)
	}
	dup := g
	dup.ID = "hg2"
	if err := st.CreateHelperGroup(ctx, "org-a", dup); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a second group for the request: %v", err)
	}
	got, err := st.GetHelperGroupByRequest(ctx, "org-a", "rq1")
	if err != nil || got.ID != "hg1" || len(got.Parts) != 2 || got.Parts[0].Text != "contrato 1" || got.Parts[0].Files[0].Path != "/Offices/Legal/a.pdf" ||
		got.CapUSD != 0.2 || got.Estimate.MaxUSD != 0.2 || got.Parallel != 3 || got.AcceptedBy != "alice" {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if err := st.AddHelperSpend(ctx, "org-a", "hg1", 0.03); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHelperSpend(ctx, "org-a", "hg1", 0.02); err != nil {
		t.Fatal(err)
	}
	got.Status, got.UnionTaskID, got.Parts[0].TaskID, got.SpentUSD = domain.HelperStopped, "t-union", "t-1", 99
	got.StopReason = domain.HelperStopCap
	if err := st.UpdateHelperGroup(ctx, "org-a", got); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetHelperGroup(ctx, "org-a", "hg1")
	if cur.SpentUSD < 0.0499 || cur.SpentUSD > 0.0501 || cur.Status != domain.HelperStopped || cur.UnionTaskID != "t-union" || cur.Parts[0].TaskID != "t-1" || cur.StopReason != domain.HelperStopCap {
		t.Fatalf("update keeps the real spend: %+v", cur)
	}
	if list, _ := st.ListHelperGroups(ctx, "org-a", domain.HelperStopped); len(list) != 1 {
		t.Fatalf("list by status = %d", len(list))
	}
	if list, _ := st.ListHelperGroups(ctx, "org-a", domain.HelperRunning); len(list) != 0 {
		t.Fatalf("list by status = %d", len(list))
	}

	// tasks keep the helper mark
	task := domain.Task{ID: "t-1", RequestID: "rq1", Title: "Ayudante 1 de 2", AgentID: "analyst", Status: domain.TaskPending, DependsOn: []string{}, CreatedAt: now,
		Helper: &domain.TaskHelper{GroupID: "hg1", Index: 1, Total: 2, Label: "Contratos 1"}}
	plain := domain.Task{ID: "t-0", RequestID: "rq1", Title: "normal", AgentID: "analyst", Status: domain.TaskPending, DependsOn: []string{}, CreatedAt: now}
	for _, tk := range []domain.Task{task, plain} {
		if err := st.CreateTask(ctx, "org-a", tk); err != nil {
			t.Fatal(err)
		}
	}
	gt, err := st.GetTask(ctx, "org-a", "t-1")
	if err != nil || gt.Helper == nil || gt.Helper.GroupID != "hg1" || gt.Helper.Index != 1 || gt.Helper.Total != 2 || gt.Helper.Label != "Contratos 1" {
		t.Fatalf("helper mark = %+v %v", gt.Helper, err)
	}
	if pt, _ := st.GetTask(ctx, "org-a", "t-0"); pt.Helper != nil {
		t.Fatal("an ordinary task has no helper mark")
	}

	// isolation
	if _, err := st.GetHelperGroup(ctx, "org-b", "hg1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads org-a's group: %v", err)
	}
	if _, err := st.GetHelperGroupByRequest(ctx, "org-b", "rq1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads org-a's group by request: %v", err)
	}
	if err := st.AddHelperSpend(ctx, "org-b", "hg1", 5); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b spends on org-a's group: %v", err)
	}
	if err := st.UpdateHelperGroup(ctx, "org-b", got); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b updates org-a's group: %v", err)
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM helper_groups WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d org-a groups", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
