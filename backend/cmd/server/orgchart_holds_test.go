package main

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/infrastructure/memory"
)

func TestHoldStoreForFallsBackToMemoryWithoutDatabase(t *testing.T) {
	if _, ok := holdStoreFor(nil).(*application.MemoryHolds); !ok {
		t.Fatal("without a database the holds are in memory")
	}
}

func TestStartHoldWatcherStopsWithContext(t *testing.T) {
	store := memory.New()
	orch := application.NewOrchestrator(context.Background(), application.DefaultConfig(), store, nil, memory.NewLocker(), nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := startHoldWatcher(ctx, orch, store, 5*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher must stop when the server shuts down")
	}
}
