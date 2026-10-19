package main

import (
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/sandbox"
)

// Setting only RENDER_* gives the agents a renderer but never makes code
// execution available.
func TestRenderEnvDoesNotEnableCodeRuns(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := memory.New()
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: st, Pub: nopPub{}, Log: log}
	orch := application.NewOrchestrator(t.Context(), application.DefaultConfig(), st, nil, memory.NewLocker(), rec, application.NewApprovals(application.DefaultConfig(), st, rec),
		&application.Queries{Store: st}, log)
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: domain.DemoOrgID})
	t.Setenv("SANDBOX_URL", "")
	t.Setenv("SANDBOX_TOKEN", "")
	t.Setenv("RENDER_URL", "http://wf-render:8090")
	t.Setenv("RENDER_TOKEN", "render-token-xyz")
	if renderFromEnv(log) == nil {
		t.Fatal("RENDER_* must configure the renderer")
	}
	if w := wireSandbox(log, nil, rec, connWiring{}, orch, dr); w.available || w.svc.Runner != nil {
		t.Fatalf("RENDER_* must not enable code execution: %+v", w)
	}
}

func renderURLOf(t *testing.T, r interface{}) string {
	t.Helper()
	dr, ok := r.(sandbox.DriveRenderer)
	if !ok {
		t.Fatalf("not a DriveRenderer: %T", r)
	}
	return dr.R.(*sandbox.HTTPRunner).BaseURL
}

func TestRenderFromEnvPrecedence(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := func(render, rtok, sbx, stok, off string) {
		t.Setenv("RENDER_URL", render)
		t.Setenv("RENDER_TOKEN", rtok)
		t.Setenv("SANDBOX_URL", sbx)
		t.Setenv("SANDBOX_TOKEN", stok)
		t.Setenv("DRIVE_RENDER", off)
	}
	set("", "", "", "", "")
	if renderFromEnv(log) != nil {
		t.Fatal("nothing configured must give no renderer")
	}
	set("http://wf-render:8090", "rt", "", "", "")
	if got := renderURLOf(t, renderFromEnv(log)); got != "http://wf-render:8090" {
		t.Fatalf("RENDER_* alone must work without SANDBOX_URL: %s", got)
	}
	set("http://wf-render:8090", "rt", "http://sandbox-runner:8090", "st", "")
	if got := renderURLOf(t, renderFromEnv(log)); got != "http://wf-render:8090" {
		t.Fatalf("the dedicated renderer wins: %s", got)
	}
	set("http://wf-render:8090", "", "http://sandbox-runner:8090", "st", "")
	if got := renderURLOf(t, renderFromEnv(log)); got != "http://sandbox-runner:8090" {
		t.Fatalf("an incomplete RENDER_* pair falls back to the runner: %s", got)
	}
	set("", "", "http://sandbox-runner:8090", "st", "")
	if got := renderURLOf(t, renderFromEnv(log)); got != "http://sandbox-runner:8090" {
		t.Fatalf("fallback to the runner: %s", got)
	}
	set("http://wf-render:8090", "rt", "http://sandbox-runner:8090", "st", "off")
	if renderFromEnv(log) != nil {
		t.Fatal("DRIVE_RENDER=off disables the renderer")
	}
}
