package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/objstore"
)

// Without SANDBOX_URL/SANDBOX_TOKEN no runner is installed (code.run is denied
// as sandbox_unavailable) but the settings still work; with both, the sandbox
// is available and wired to the drive.
func TestWireSandbox(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := memory.New()
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: st, Pub: nopPub{}, Log: log}
	orch := application.NewOrchestrator(t.Context(), application.DefaultConfig(), st, nil, memory.NewLocker(), rec, application.NewApprovals(application.DefaultConfig(), st, rec),
		&application.Queries{Store: st}, log)
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: domain.DemoOrgID})

	t.Setenv("SANDBOX_URL", "")
	t.Setenv("SANDBOX_TOKEN", "")
	w := wireSandbox(log, nil, rec, connWiring{}, orch, dr)
	if w.svc == nil || w.available || w.svc.Runner != nil {
		t.Fatalf("without a runner: %+v", w)
	}
	if s, err := w.svc.Settings(t.Context(), domain.DemoOrgID); err != nil || s.Enabled {
		t.Fatalf("code execution must be off by default: %+v %v", s, err)
	}

	t.Setenv("SANDBOX_URL", "http://sandbox-runner:8090")
	t.Setenv("SANDBOX_TOKEN", "secret-token-xyz")
	w = wireSandbox(log, nil, rec, connWiring{}, orch, dr)
	if !w.available || w.svc.Runner == nil || w.svc.Files == nil || w.svc.Sink == nil {
		t.Fatalf("with a runner: %+v", w)
	}
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }
