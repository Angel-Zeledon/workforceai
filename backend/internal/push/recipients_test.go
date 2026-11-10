package push

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// E0: an approval goes to the people who can decide it, a request's result to the person who asked.
func TestPushGoesToThePersonItIsFor(t *testing.T) {
	cap, client := fakePushService(t, http.StatusCreated)
	store := NewMemoryStore()
	svc := newTestService(t, client, store)
	ctx := context.Background()
	for _, u := range []string{"owner", "admin", "member", "asker"} {
		_, p256, _, auth := browserKeys(t)
		if _, err := svc.Subscribe(ctx, Subscription{OrgID: "o", UserID: u, Endpoint: "https://push.test/" + u, P256dh: p256, Auth: auth}); err != nil {
			t.Fatal(err)
		}
	}
	who := &Recipients{
		Members: func(context.Context, string) ([]Member, error) {
			return []Member{{"owner", "owner"}, {"admin", "admin"}, {"member", "member"}, {"asker", "admin"}}, nil
		},
		// the deciders are owners and admins, and nobody decides what they asked for
		CanDecide: func(_ context.Context, _ string, ap domain.Approval, userID, role string) bool {
			return (role == "owner" || role == "admin") && userID != ap.RequestedBy
		},
	}
	n := Notifier{Inner: nopPub{}, Svc: svc, Who: who}
	sentTo := func(want int) []string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			cap.mu.Lock()
			got := len(cap.reqs)
			paths := []string{}
			for _, r := range cap.reqs {
				paths = append(paths, r.URL.Path[1:])
			}
			cap.mu.Unlock()
			if got >= want {
				time.Sleep(100 * time.Millisecond) // nothing more may arrive
				cap.mu.Lock()
				defer cap.mu.Unlock()
				paths = paths[:0]
				for _, r := range cap.reqs {
					paths = append(paths, r.URL.Path[1:])
				}
				cap.reqs, cap.body = nil, nil
				slices.Sort(paths)
				return paths
			}
			if time.Now().After(deadline) {
				t.Fatalf("got %d pushes, want %d", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	ap := domain.Approval{ID: "ap", Title: "pagar", Risk: "high", RequestedBy: "asker", Status: domain.ApprovalPending}
	_ = n.Publish(ctx, domain.Event{Type: domain.EvApprovalRequest, OrgID: "o", Payload: map[string]any{"approval": ap}})
	if got := sentTo(2); !slices.Equal(got, []string{"admin", "owner"}) {
		t.Fatalf("approval pushed to %v", got)
	}
	_ = n.Publish(ctx, domain.Event{Type: domain.EvRequestCompleted, OrgID: "o", Payload: map[string]any{"request_id": "r1", "requested_by": "member"}})
	if got := sentTo(1); !slices.Equal(got, []string{"member"}) {
		t.Fatalf("result pushed to %v", got)
	}
	_ = n.Publish(ctx, domain.Event{Type: domain.EvRequestFailed, OrgID: "o", Payload: map[string]any{"request_id": "r2", "requested_by": "asker"}})
	if got := sentTo(1); !slices.Equal(got, []string{"asker"}) {
		t.Fatalf("failure pushed to %v", got)
	}
	// needs your help: to the person asked; nobody asked -> the owners and admins
	_ = n.Publish(ctx, domain.Event{Type: domain.EvHelpNotice, OrgID: "o", Payload: map[string]any{"asked_for": "member", "count": 3}})
	if got := sentTo(1); !slices.Equal(got, []string{"member"}) {
		t.Fatalf("help pushed to %v", got)
	}
	_ = n.Publish(ctx, domain.Event{Type: domain.EvHelpNotice, OrgID: "o", Payload: map[string]any{"asked_for": "", "count": 1}})
	if got := sentTo(3); !slices.Equal(got, []string{"admin", "asker", "owner"}) {
		t.Fatalf("unowned help pushed to %v", got)
	}
	// nobody asked (scheduled work): no push at all
	_ = n.Publish(ctx, domain.Event{Type: domain.EvRequestCompleted, OrgID: "o", Payload: map[string]any{"request_id": "r3"}})
	time.Sleep(200 * time.Millisecond)
	cap.mu.Lock()
	if len(cap.reqs) != 0 {
		t.Fatalf("a result without a requester was pushed to %d browsers", len(cap.reqs))
	}
	cap.mu.Unlock()
}
