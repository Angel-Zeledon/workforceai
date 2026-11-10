package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/sandbox"
)

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
// fs is the agents' folder policy: code.run inputs obey the same zones as fs.read (nil: the drive without projects).
func wireSandbox(log *slog.Logger, pg *postgres.Store, rec *application.Recorder, cw connWiring, orch *application.Orchestrator, dsvc *drive.Service, fs *drive.AgentFS) sandboxWiring {
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
		if fs == nil {
			fs = &drive.AgentFS{Svc: dsvc}
		}
		df := &sandbox.DriveFiles{Drive: dsvc, FS: fs, Notify: func(ctx context.Context, org, agentID, p string) {
			octx := application.WithOrg(ctx, org) // inside an office's folder: only its members hear of it
			rec.Emit(octx, fileActivity(octx, dsvc, agentID, map[string]any{"agent_id": agentID, "action": "write", "path": p}))
		}}
		svc.Files, svc.Sink = df, df
	}
	orch.SetCodeRunner(svc)
	log.Info("code sandbox ready: every run needs a human approval unless the organization opted into desk mode", "runner", url)
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

// sandboxFolders is the folder policy of code.run inputs: the same as the fs.* tools (projects and customers).
func sandboxFolders(dsvc *drive.Service, pstore projects.Store, cust *customers.Service) *drive.AgentFS {
	if dsvc == nil {
		return nil
	}
	return &drive.AgentFS{Svc: dsvc, Projects: projectDirectory{store: pstore}, Customers: customerDirectory(cust)}
}
