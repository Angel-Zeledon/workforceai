package needsyou

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

func src(kind string, items ...Item) Source {
	return Source{Kind: kind, Collect: func(context.Context, bool) ([]Item, error) { return items, nil }}
}

func ids(items []Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestCollectOrdersByUrgencyThenOldest(t *testing.T) {
	res := Collect(context.Background(), Options{Full: true, Now: t0},
		src(KindSuggestion, Item{ID: "s-old", CreatedAt: t0.Add(-72 * time.Hour)}),
		src(KindApproval,
			Item{ID: "a-low-new", Risk: "low", CreatedAt: t0.Add(-time.Minute)},
			Item{ID: "a-high", Risk: "high", CreatedAt: t0.Add(-time.Minute)},
			Item{ID: "a-low-old", Risk: "low", CreatedAt: t0.Add(-time.Hour)},
			Item{ID: "a-stale", Risk: "low", CreatedAt: t0.Add(-25 * time.Hour)}),
		src(KindQuestion, Item{ID: "q", CreatedAt: t0.Add(-30 * time.Minute)}),
		src(KindTrust, Item{ID: "tp", CreatedAt: t0.Add(-48 * time.Hour)}),
	)
	want := []string{"a-stale", "a-high", "a-low-old", "q", "a-low-new", "s-old", "tp"}
	if got := ids(res.Items); !equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if res.Total != 7 || res.Counts[KindApproval] != 4 || res.Counts[KindQuestion] != 1 || res.Counts[KindSuggestion] != 1 || res.Counts[KindTrust] != 1 {
		t.Fatalf("counts = %d %v", res.Total, res.Counts)
	}
	for _, it := range res.Items {
		if it.Kind == "" || it.Urgency == "" {
			t.Fatalf("item without kind or urgency: %+v", it)
		}
	}
	if res.Items[0].Urgency != UrgencyHigh || res.Items[len(res.Items)-1].Urgency != UrgencyLow {
		t.Fatalf("urgency: %+v", res.Items)
	}
}

func TestDoubleApprovalIsUrgent(t *testing.T) {
	if u := Urgency(Item{Kind: KindApproval, Risk: "low", Double: true, CreatedAt: t0}, t0); u != UrgencyHigh {
		t.Fatalf("double approval urgency = %s", u)
	}
}

func TestFailingSourceIsLeftOutAndNamed(t *testing.T) {
	bad := Source{Kind: KindTrust, Collect: func(context.Context, bool) ([]Item, error) { return []Item{{ID: "leak"}}, errors.New("boom") }}
	res := Collect(context.Background(), Options{Now: t0}, src(KindApproval, Item{ID: "a", CreatedAt: t0}), bad)
	if got := ids(res.Items); !equal(got, []string{"a"}) {
		t.Fatalf("a failing source leaked items: %v", got)
	}
	if len(res.Unavailable) != 1 || res.Unavailable[0] != KindTrust || res.Counts[KindTrust] != 0 {
		t.Fatalf("unavailable = %v counts %v", res.Unavailable, res.Counts)
	}
}

func TestLimitCutsItemsNotCounts(t *testing.T) {
	res := Collect(context.Background(), Options{Now: t0, Limit: 2}, src(KindApproval,
		Item{ID: "a1", CreatedAt: t0.Add(-3 * time.Minute)}, Item{ID: "a2", CreatedAt: t0.Add(-2 * time.Minute)}, Item{ID: "a3", CreatedAt: t0.Add(-time.Minute)}))
	if got := ids(res.Items); !equal(got, []string{"a1", "a2"}) || res.Total != 3 || !res.Truncated {
		t.Fatalf("limit: %v total %d truncated %v", got, res.Total, res.Truncated)
	}
}

func TestCountOnlyReturnsNoItems(t *testing.T) {
	var full []bool
	s := Source{Kind: KindApproval, Collect: func(_ context.Context, f bool) ([]Item, error) {
		full = append(full, f)
		return []Item{{ID: "a", CreatedAt: t0}}, nil
	}}
	res := Collect(context.Background(), Options{Now: t0, CountOnly: true}, s)
	if res.Total != 1 || len(res.Items) != 0 || len(full) != 1 || full[0] {
		t.Fatalf("count only: %+v (full=%v)", res, full)
	}
}

func TestNilSourceAndEmptyKindAreIgnored(t *testing.T) {
	res := Collect(context.Background(), Options{Now: t0}, Source{Kind: KindApproval}, src("", Item{ID: "x"}))
	if res.Total != 0 || len(res.Items) != 0 || res.Items == nil {
		t.Fatalf("ignored sources: %+v", res)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
