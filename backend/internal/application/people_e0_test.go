package application_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// E0 "my approvals": the inbox marks and filters what the reader asked for and what they can decide.
func TestApprovalInboxMine(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	asked := pend("a1", "", "pay", "low", now)
	asked.RequestedBy, asked.NoSelfApproval = "luis", true
	other := pend("a2", "", "send", "low", now)
	other.RequestedBy = "ana"
	ownerOnly := pend("a3", "", "delete", "low", now)
	ownerOnly.RequiredRole = "owner"
	in := application.InboxInput{Pending: []domain.Approval{asked, other, ownerOnly}, Now: now, Viewer: "luis", ViewerRole: "admin", ViewerCanDecide: true}

	all := application.BuildApprovalInbox(in)
	if all.Counts != (application.InboxCounts{All: 3, Requested: 1, Decidable: 1}) || len(all.Items) != 3 {
		t.Fatalf("counts = %+v (%d items)", all.Counts, len(all.Items))
	}
	in.Mine = application.InboxMineRequested
	if got := application.BuildApprovalInbox(in); len(got.Items) != 1 || got.Items[0].ID != "a1" || !got.Items[0].RequestedByMe || got.Items[0].CanDecide {
		t.Fatalf("requested = %+v", got.Items)
	}
	in.Mine = application.InboxMineDecidable // a1: no self-approval; a3: owner only
	if got := application.BuildApprovalInbox(in); len(got.Items) != 1 || got.Items[0].ID != "a2" || !got.Items[0].CanDecide {
		t.Fatalf("decidable = %+v", got.Items)
	}
	// a member (no approvals:decide) can decide nothing
	in.ViewerRole, in.ViewerCanDecide = "member", false
	if got := application.BuildApprovalInbox(in); len(got.Items) != 0 || got.Counts.Decidable != 0 {
		t.Fatalf("member decidable = %+v", got.Items)
	}
}

// E0: every cost entry records the person whose action started the call; system work records nobody.
func TestUsageEntriesRecordThePerson(t *testing.T) {
	st := memory.New()
	cfg := application.Config{OrgID: "demo"}
	rec := &application.Recorder{OrgID: "demo", Store: st, Pub: nopPublisher{}, Log: slog.Default()}
	b := application.NewBudget(cfg, st, rec, slog.Default())
	ctx := application.WithActor(context.Background(), "user-123")
	if _, err := b.Record(ctx, "", domain.UsageEntry{AgentID: "legal", Kind: domain.UsageChat}, 0.01); err != nil {
		t.Fatal(err)
	}
	for _, sys := range []string{"system", "agent:legal", "link:a@b.c", ""} {
		if _, err := b.Record(application.WithActor(context.Background(), sys), "", domain.UsageEntry{AgentID: "legal", Kind: domain.UsageChat}, 0.01); err != nil {
			t.Fatal(err)
		}
	}
	us, _ := st.ListUsage(ctx, "demo")
	if len(us) != 5 || us[0].UserID != "user-123" {
		t.Fatalf("first entry = %+v", us)
	}
	for _, u := range us[1:] {
		if u.UserID != "" {
			t.Fatalf("a non-person actor was recorded as a person: %+v", u)
		}
	}
}

type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, domain.Event) error { return nil }
