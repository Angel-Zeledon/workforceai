package application_test

import (
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func pend(id, task, action, risk string, at time.Time) domain.Approval {
	return domain.Approval{ID: id, TaskID: task, AgentID: "sales", Action: action, Risk: risk, Status: domain.ApprovalPending, CreatedAt: at, RequiredApprovals: 1}
}

func TestBuildApprovalInboxGroupsOnlyWhatTheBatchAPIAllows(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	tasks := map[string]domain.Task{
		"t1": {ID: "t1", Title: "Categorize", RequestID: "rq-a", CostUSD: 0.0123},
		"t2": {ID: "t2", Title: "Categorize 2", RequestID: "rq-a"},
		"t3": {ID: "t3", Title: "Send", RequestID: "rq-a"},
		"t4": {ID: "t4", Title: "Publish", RequestID: "rq-b"},
		"t5": {ID: "t5", Title: "Publish 2", RequestID: "rq-b"},
		"t6": {ID: "t6", Title: "Alone", RequestID: "rq-none"},
	}
	dbl := pend("p5", "t5", "publish", "low", now.Add(-time.Minute))
	dbl.RequiredApprovals = 2
	pending := []domain.Approval{
		pend("p1", "t1", "categorize", "low", now.Add(-3*time.Hour)),
		pend("p2", "t2", "categorize", "low", now.Add(-2*time.Hour)),
		pend("p3", "t3", "send_email", "high", now.Add(-time.Hour)),
		pend("p4", "t4", "publish", "low", now.Add(-time.Hour)),
		dbl,
		pend("p6", "t6", "categorize", "medium", now),
	}
	inbox := application.BuildApprovalInbox(application.InboxInput{
		Pending: pending, Tasks: tasks, Now: now,
		ProjectOf:   func(r string) string { return map[string]string{"rq-a": "prj-a", "rq-b": "prj-b"}[r] },
		ProjectName: func(id string) string { return "Project " + id },
		Counter: func(id string) (domain.Counterargument, bool) {
			if id == "p3" {
				return domain.Counterargument{Counterargument: "Wait", Risks: []string{"a", "b", "c", "d"}}, true
			}
			return domain.Counterargument{}, false
		},
	})
	if len(inbox.Items) != 6 || inbox.Items[0].ID != "p3" || !inbox.Items[0].HighRisk || inbox.Items[0].Counterargument == nil || len(inbox.Items[0].Counterargument.Risks) != 3 {
		t.Fatalf("items = %+v", inbox.Items)
	}
	if inbox.Items[1].ID != "p6" { // medium before low
		t.Fatalf("order = %s", inbox.Items[1].ID)
	}
	byKey := map[string]application.InboxGroup{}
	for _, g := range inbox.Groups {
		byKey[g.Key] = g
	}
	if g := byKey["prj-a|categorize"]; g.Count != 2 || !g.Batchable || g.ProjectName != "Project prj-a" {
		t.Fatalf("low-risk group = %+v", g)
	}
	if g := byKey["prj-b|publish"]; g.Count != 2 || g.Batchable || g.Reason != "risk" {
		t.Fatalf("double approval group = %+v", g)
	}
	if len(inbox.Groups) != 2 { // a single item or an item without project is not a group
		t.Fatalf("groups = %+v", inbox.Groups)
	}
	for _, it := range inbox.Items {
		if it.ID == "p1" && (it.TaskCostUSD != 0.0123 || it.ProjectID != "prj-a" || it.GroupKey != "prj-a|categorize") {
			t.Fatalf("p1 = %+v", it)
		}
		if (it.ID == "p3" || it.ID == "p6") && it.GroupKey != "" {
			t.Fatalf("%s should not be grouped", it.ID)
		}
	}
	if inbox.Rules.Undo != "client_side" || inbox.Rules.SuggestMinDecisions != 20 {
		t.Fatalf("rules = %+v", inbox.Rules)
	}
}

func TestApprovalInboxSuggestionIsConservative(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var hist []domain.Approval
	add := func(action, risk string, n, rejected int, at time.Time, by string) {
		for i := 0; i < n; i++ {
			st := domain.ApprovalApproved
			if i < rejected {
				st = domain.ApprovalRejected
			}
			ts := at
			hist = append(hist, domain.Approval{ID: fmt.Sprintf("%s-%d-%s", action, i, by), Action: action, Risk: risk, Status: st, ResolvedAt: &ts,
				Decisions: []domain.ApprovalDecision{{By: by}}})
		}
	}
	recent := now.Add(-24 * time.Hour)
	add("draft_reply", "low", 40, 1, recent, "ana")              // 97.5 %: suggested
	add("categorize", "low", 19, 0, recent, "ana")               // too few decisions
	add("publish", "medium", 20, 1, recent, "ana")               // exactly 95 %: not above the bar
	add("pay_invoice", "high", 30, 0, recent, "ana")             // high risk: never
	add("old_kind", "low", 30, 0, now.AddDate(0, 0, -40), "ana") // outside the window
	add("auto", "low", 30, 0, recent, "standing:sa-1")           // standing approvals are not human decisions
	got := application.BuildApprovalInbox(application.InboxInput{History: hist, Now: now}).Suggestions
	if len(got) != 1 || got[0].Action != "draft_reply" || got[0].Decided != 40 || got[0].Approved != 39 || got[0].Rate != 0.975 || got[0].Kind != "raise_notify_threshold" {
		t.Fatalf("suggestions = %+v", got)
	}
}
