package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOrgQueueLimitAndPriorityOrder(t *testing.T) {
	var q orgQueue
	ctx := context.Background()
	r1, err := q.acquire(ctx, "o", 1, PriorityInteractive, nil)
	if err != nil {
		t.Fatal(err)
	}
	// another organization is not affected
	r2, err := q.acquire(ctx, "other", 1, PrioritySchedule, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2()

	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	start := func(name string, p WorkPriority) {
		queued := make(chan int, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := q.acquire(ctx, "o", 1, p, func(ahead int) { queued <- ahead })
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			rel()
		}()
		select {
		case <-queued:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not queue", name)
		}
	}
	start("schedule", PrioritySchedule)
	start("project-1", PriorityProject)
	start("interactive", PriorityInteractive)
	start("project-2", PriorityProject)
	if inUse, queued := q.stats("o"); inUse != 1 || queued != 4 {
		t.Fatalf("stats = %d in use, %d queued", inUse, queued)
	}
	r1()
	r1() // idempotent
	wg.Wait()
	want := []string{"interactive", "project-1", "project-2", "schedule"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if inUse, queued := q.stats("o"); inUse != 0 || queued != 0 {
		t.Fatalf("slots leaked: %d in use, %d queued", inUse, queued)
	}
}

func TestOrgQueueCancelledWaiterLeavesAndNoCapWhenZero(t *testing.T) {
	var q orgQueue
	rel, _ := q.acquire(context.Background(), "o", 1, PriorityInteractive, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.acquire(ctx, "o", 1, PriorityInteractive, func(int) { cancel() })
		done <- err
	}()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, queued := q.stats("o"); queued != 0 {
		t.Fatal("cancelled waiter still queued")
	}
	rel()
	if inUse, _ := q.stats("o"); inUse != 0 {
		t.Fatal("slot leaked")
	}
	for i := 0; i < 100; i++ { // limit <= 0: no cap, never waits
		if _, err := q.acquire(context.Background(), "o", 0, PrioritySchedule, func(int) { t.Fatal("queued without cap") }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOrgQueueStressNeverExceedsLimit(t *testing.T) {
	var q orgQueue
	const limit = 3
	var mu sync.Mutex
	cur, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			if i%7 == 0 { // some give up while waiting
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
				defer cancel()
			}
			rel, err := q.acquire(ctx, "o", limit, WorkPriority(i%3), nil)
			if err != nil {
				return
			}
			mu.Lock()
			cur++
			peak = max(peak, cur)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			rel()
		}(i)
	}
	wg.Wait()
	if peak > limit {
		t.Fatalf("peak %d > limit %d", peak, limit)
	}
	if inUse, queued := q.stats("o"); inUse != 0 || queued != 0 {
		t.Fatalf("slots leaked: %d in use, %d queued", inUse, queued)
	}
}

func TestWorkPriorityContext(t *testing.T) {
	if WorkPriorityFrom(context.Background()) != PriorityInteractive {
		t.Fatal("default must be interactive")
	}
	ctx := WithWorkPriority(context.Background(), PrioritySchedule)
	if WorkPriorityFrom(ctx) != PrioritySchedule || PrioritySchedule.String() != "schedule" || PriorityProject.String() != "project" {
		t.Fatal("priority round trip")
	}
}

// Requests take turns within one priority: a request that arrives behind a long
// backlog of another one is served after one turn, not after the whole backlog.
func TestOrgQueueRequestsTakeTurns(t *testing.T) {
	var q orgQueue
	base := context.Background()
	a, b := WithRequestID(base, "req-a"), WithRequestID(base, "req-b")
	hold, err := q.acquire(a, "o", 1, PriorityProject, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	start := func(ctx context.Context, name string) {
		queued := make(chan int, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := q.acquire(ctx, "o", 1, PriorityProject, func(ahead int) { queued <- ahead })
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			rel()
		}()
		select {
		case <-queued:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not queue", name)
		}
	}
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		start(a, n)
	}
	start(b, "b1")
	start(b, "b2")
	hold()
	wg.Wait()
	// a had the slot (so b goes first), then they alternate, each keeping its own arrival order
	want := []string{"b1", "a1", "b2", "a2", "a3", "a4"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
