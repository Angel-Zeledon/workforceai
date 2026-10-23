package customers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
)

func TestHealthOf(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	old, recent := now.Add(-30*24*time.Hour), now.Add(-time.Hour)
	cases := []struct {
		name string
		p    []customers.ProjectInfo
		r    []customers.RequestInfo
		want string
	}{
		{"nothing", nil, nil, customers.StatusIdle},
		{"finished work only", []customers.ProjectInfo{{Status: "done"}}, []customers.RequestInfo{{Status: "done", CreatedAt: recent}}, customers.StatusIdle},
		{"running project", []customers.ProjectInfo{{Status: "running"}}, nil, customers.StatusOnTrack},
		{"running request", nil, []customers.RequestInfo{{Status: "running", CreatedAt: recent}}, customers.StatusOnTrack},
		{"project gate", []customers.ProjectInfo{{Status: "waiting_human"}}, nil, customers.StatusWaitingApproval},
		{"pending approval in a running project", []customers.ProjectInfo{{Status: "running", Awaiting: 2}}, nil, customers.StatusWaitingApproval},
		{"request awaiting approval", nil, []customers.RequestInfo{{Status: "awaiting_approval", CreatedAt: recent}}, customers.StatusWaitingApproval},
		{"failed task beats waiting", []customers.ProjectInfo{{Status: "running", Awaiting: 1, Failed: 1}}, nil, customers.StatusBlocked},
		{"paused project", []customers.ProjectInfo{{Status: "paused"}}, nil, customers.StatusBlocked},
		{"recent failed project", []customers.ProjectInfo{{Status: "failed", FinishedAt: &recent}}, nil, customers.StatusBlocked},
		{"old failure is history", []customers.ProjectInfo{{Status: "failed", FinishedAt: &old}}, []customers.RequestInfo{{Status: "failed", CreatedAt: old}}, customers.StatusIdle},
		{"recent failed request", nil, []customers.RequestInfo{{Status: "failed", CreatedAt: recent}}, customers.StatusBlocked},
	}
	for _, c := range cases {
		if got := customers.HealthOf(c.p, c.r, now); got.Status != c.want {
			t.Errorf("%s: got %s want %s (%+v)", c.name, got.Status, c.want, got)
		}
	}
	h := customers.HealthOf([]customers.ProjectInfo{{Status: "running", Awaiting: 2}, {Status: "waiting_human"}, {Status: "done"}}, nil, now)
	if h.ProjectsActive != 2 || h.ApprovalsWaiting != 3 {
		t.Fatalf("counts: %+v", h)
	}
}

func TestServiceValidationGateAndAudit(t *testing.T) {
	ctx := application.WithOrg(context.Background(), org)
	var audit []string
	readOnly := false
	svc := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org,
		WriteGate: func(context.Context, string) error {
			if readOnly {
				return errors.New("read-only mode")
			}
			return nil
		},
		Agents: func(context.Context, string) ([]domain.Agent, error) {
			return []domain.Agent{{ID: "accounting", Title: "Contador"}}, nil
		},
		Audit: func(_ context.Context, _, actor, action, _ string, _ map[string]any) {
			audit = append(audit, actor+":"+action)
		}})
	for _, in := range []customers.Input{{}, {Name: ptr("   ")}, {Name: ptr("x"), Color: ptr("#12")}, {Name: ptr(string(make([]rune, 121)))}} {
		if _, err := svc.Create(ctx, "ana", in); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid input accepted: %+v -> %v", in, err)
		}
	}
	c, err := svc.Create(ctx, "ana", customers.Input{Name: ptr("Ferretería​ López\n")})
	if err != nil || c.Name != "Ferretería López" || c.Color == "" {
		t.Fatalf("name cleaned and color defaulted: %+v %v", c, err)
	}
	a, err := svc.AddAssignment(ctx, "ana", c.ID, customers.AssignmentInput{AgentID: "accounting"})
	if err != nil || a.DisplayName != "Contador · Ferretería López" {
		t.Fatalf("clone display name: %+v %v", a, err)
	}
	if ids, _ := svc.AssignedAgents(ctx, org, c.ID); len(ids) != 1 || ids[0] != "accounting" {
		t.Fatalf("assigned agents: %v", ids)
	}
	if _, err := svc.LinkFolder(ctx, "ana", c.ID, "fld_x"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("without a drive folders cannot be linked: %v", err)
	}
	readOnly = true
	if _, err := svc.Create(ctx, "ana", customers.Input{Name: ptr("Otra")}); err == nil {
		t.Fatal("read-only mode must refuse writes")
	}
	if err := svc.Archive(ctx, "ana", c.ID); err == nil {
		t.Fatal("read-only mode must refuse archive")
	}
	readOnly = false
	if err := svc.Archive(ctx, "ana", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.LinkRequest(ctx, org, "rq-1", c.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived customers take no new work: %v", err)
	}
	if _, err := svc.AddAssignment(ctx, "ana", c.ID, customers.AssignmentInput{AgentID: "accounting"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived customers get no clones: %v", err)
	}
	if cid, err := svc.CustomerOfRequest(ctx, org, "rq-unknown"); cid != "" || err != nil {
		t.Fatalf("an unlinked request is unscoped: %q %v", cid, err)
	}
	want := []string{"ana:customer.created", "ana:customer.clone.created", "ana:customer.archived"}
	if len(audit) != len(want) {
		t.Fatalf("audit: %v", audit)
	}
	for i := range want {
		if audit[i] != want[i] {
			t.Fatalf("audit: %v", audit)
		}
	}
}

func TestLinkFolderRefusesTopLevelAndForeignOrg(t *testing.T) {
	w := newWorld(t)
	top, err := w.drv.FolderByPath(w.ctx, "/Shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.cust.LinkFolder(w.ctx, "ana", w.a.ID, top.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a top-level folder cannot belong to one customer: %v", err)
	}
	sub, _ := w.drv.FolderByPath(w.ctx, "/Shared/Panaderia")
	if _, err := w.cust.LinkFolder(w.ctx, "ana", w.a.ID, sub.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a folder belongs to one customer at a time: %v", err)
	}
	// Another organization cannot see the customers of this one.
	other := application.WithOrg(context.Background(), "o2")
	if _, err := w.cust.Get(other, w.a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("customers are per organization: %v", err)
	}
}
