package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/sandbox"
)

// driveFiles adapts the drive to the sandbox: it feeds the approved input files
// and stores the produced ones on the desk of the agent that asked for the run.
type driveFiles struct {
	svc    *drive.Service
	notify func(ctx context.Context, org, agentID, path string)
}

var (
	_ sandbox.FileSource = (*driveFiles)(nil)
	_ sandbox.FileSink   = (*driveFiles)(nil)
)

// Read implements sandbox.FileSource. ref is a file id or an absolute drive
// path. The organization comes from the request, never from the reference: a
// file of another organization is simply "not found".
func (d *driveFiles) Read(ctx context.Context, org, ref string) (string, io.ReadCloser, error) {
	ctx = application.WithOrg(ctx, org)
	var f drive.File
	var err error
	if strings.HasPrefix(ref, "/") {
		f, err = d.svc.FileByPath(ctx, ref)
	} else {
		f, err = d.svc.GetFile(ctx, ref)
	}
	if err != nil {
		return "", nil, domain.ErrNotFound
	}
	if f.DeletedAt != nil {
		return "", nil, domain.ErrNotFound
	}
	rc, _, err := d.svc.Open(ctx, f, 0)
	if err != nil {
		return "", nil, err
	}
	return f.Name, rc, nil
}

// WriteOutput implements sandbox.FileSink: /Agents/<agent>/outputs/<run id>/<name>,
// written as the agent (a new version when the file exists). The returned
// reference is the path in the drive.
func (d *driveFiles) WriteOutput(ctx context.Context, org, agentID, folderHint, name string, r io.Reader) (string, error) {
	ctx = application.WithOrg(ctx, org)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	desk, err := d.svc.EnsureOwnedFolder(ctx, drive.RootAgents, agentID, agentID)
	if err != nil {
		return "", err
	}
	// The hint is only a suggestion (never trusted as a path): keep its last
	// segment, which is the run id, under the desk's outputs folder.
	sub := path.Base(strings.Trim(folderHint, "/"))
	p := desk.Path + "/outputs/" + sub + "/" + name
	res, err := d.svc.Write(ctx, drive.AgentP(agentID), drive.WriteInput{Path: p, Data: data, Message: "code.run output", Source: "code_run"})
	if err != nil {
		return "", err
	}
	if d.notify != nil {
		d.notify(ctx, org, agentID, res.File.Path)
	}
	return res.File.Path, nil
}

// sandboxWiring is the code sandbox as the server sees it.
type sandboxWiring struct {
	svc *sandbox.Service
	// available is true when a runner is configured (SANDBOX_URL + SANDBOX_TOKEN):
	// without it settings can be read and edited but nothing ever runs.
	available bool
}

// wireSandbox builds the code sandbox. The service always exists (settings and
// the run history work without a runner); it is installed in the orchestrator,
// and code.run can be approved, only when SANDBOX_URL and SANDBOX_TOKEN are set.
func wireSandbox(log *slog.Logger, pg *postgres.Store, rec *application.Recorder, cw connWiring, orch *application.Orchestrator, dsvc *drive.Service) sandboxWiring {
	var store sandbox.Store = sandbox.NewMemoryStore()
	if pg != nil {
		store = &postgres.CodeRunStore{S: pg}
	}
	svc := &sandbox.Service{Store: store, Suspects: cw.sus, Log: log,
		Ceiling: sandboxCeiling(), MaxRunsPerDay: envPositive("SANDBOX_MAX_RUNS_PER_DAY", sandbox.DefaultMaxRunsPerDay),
		Audit: func(ctx context.Context, org, actor, action, entity, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: d})
		},
		Emit: func(ctx context.Context, org, typ string, p map[string]any) {
			agent, _ := p["agent_id"].(string)
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, AgentID: agent, Entity: "code_run", EntityID: str(p["run_id"]), Payload: p, SkipAudit: true})
		}}
	if cw.gw != nil {
		svc.Guard = gateway.Guard{G: cw.gw}
	}
	url, token := strings.TrimSpace(os.Getenv("SANDBOX_URL")), os.Getenv("SANDBOX_TOKEN")
	if url == "" || token == "" {
		log.Info("SANDBOX_URL/SANDBOX_TOKEN not set: agents cannot run code (code.run is denied: sandbox_unavailable)")
		return sandboxWiring{svc: svc}
	}
	svc.Runner = sandbox.NewHTTPRunner(url, token)
	if dsvc != nil {
		df := &driveFiles{svc: dsvc, notify: func(ctx context.Context, org, agentID, p string) {
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: "file.activity", AgentID: agentID, Entity: "file", SkipAudit: true,
				Payload: map[string]any{"agent_id": agentID, "action": "write", "path": p}})
		}}
		svc.Files, svc.Sink = df, df
	}
	orch.SetCodeRunner(svc)
	log.Info("code sandbox ready: every run needs a human approval", "runner", url)
	return sandboxWiring{svc: svc, available: true}
}

func str(v any) string { s, _ := v.(string); return s }

func envPositive(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

// sandboxCeiling are the platform limits of one run (the runner applies its own
// ceilings too; organizations can only lower these).
func sandboxCeiling() sandbox.Limits {
	l := sandbox.DefaultLimits()
	l.TimeoutSeconds = envPositive("SANDBOX_MAX_RUNTIME_SECONDS", l.TimeoutSeconds)
	l.MemoryMB = envPositive("SANDBOX_MEMORY_MB", l.MemoryMB)
	l.PIDs = envPositive("SANDBOX_PIDS", l.PIDs)
	l.MaxOutputBytes = int64(envPositive("SANDBOX_MAX_OUTPUT_BYTES", int(l.MaxOutputBytes)))
	l.MaxOutputFileBytes = int64(envPositive("SANDBOX_MAX_OUTPUT_FILE_BYTES", int(l.MaxOutputFileBytes)))
	return l
}
