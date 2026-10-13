package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/projects"
)

type workspaceWiring struct {
	pstore    projects.Store
	projects  *projects.Service
	artifacts *artifacts.Service
	drive     *drive.Service
	sandbox   sandboxWiring
	objects   objstore.Store // where file bytes live (S3, Postgres bytea or memory)
	docIndex  *docindex.Service // passage search over the drive (bound and started in main)
}

// projectLookup lets the artifacts service name a project without importing the
// projects service (the two are built in a cycle: projects feed the workspace).
type projectLookup func(ctx context.Context, id string) (string, string, error)

func (f projectLookup) Info(ctx context.Context, id string) (string, string, error) {
	return f(ctx, id)
}

// readOnlyGate refuses artifact file uploads while the organization is in
// read-only mode; an unreadable controls state also refuses (fail closed).
func readOnlyGate(ctl *controls.Service) func(ctx context.Context, org string) error {
	if ctl == nil {
		return nil
	}
	return func(ctx context.Context, org string) error {
		st, err := ctl.State(ctx, org)
		if err != nil || st.Mode == controls.ModeReadOnly {
			return artifacts.ErrReadOnly
		}
		return nil
	}
}

// wireWorkspaces builds the projects service (whole workflows launched as real
// orchestrator tasks) and the artifacts service (agent workspaces), with
// Postgres storage when a database is configured and memory otherwise.
func wireWorkspaces(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rt application.Runtime,
	rec *application.Recorder, approvals *application.Approvals, orch *application.Orchestrator, orgCfg *application.OrgConfig, cw connWiring, modelPolicy *application.ModelPolicyService) workspaceWiring {

	var pstore projects.Store = projects.NewMemStore()
	var astore artifacts.Store = artifacts.NewMemStore()
	var collab artifacts.CollabStore = artifacts.NewMemCollab()
	var blobs artifacts.BlobStore = artifacts.NewMemBlobs()
	objects := objectStoreFromEnv(ctx, log, pg)
	if pg != nil {
		as := &postgres.ArtifactStore{S: pg}
		if _, isPG := objects.(*postgres.ObjectStore); !isPG {
			as.Objects = objects // new PDF files go to S3 when configured; old bytea blobs stay readable
		}
		pstore, astore, collab, blobs = &postgres.ProjectStore{S: pg}, as, as, as
	}
	var guard application.ExecutionGuard
	if cw.gw != nil {
		guard = gateway.Guard{G: cw.gw}
	}
	var psvc *projects.Service
	dsvc, docIdx := wireDrive(ctx, cfg, log, pg, store, rec, cw, pstore, objects, orch)
	if cfg.EnableDemoReset { // demo/dev deployments only: the demo company gets business documents
		seedDemoShared(ctx, dsvc, cfg.App.OrgID, log)
	}
	asvc := artifacts.New(artifacts.Config{Store: astore, Collab: collab, Blobs: blobs, WriteGate: readOnlyGate(cw.ctl), Rec: rec, Core: store, Asker: orch, OrgID: cfg.App.OrgID, Log: log,
		Projects: projectLookup(func(ctx context.Context, id string) (string, string, error) { return psvc.Info(ctx, id) })})
	pcfg := projects.Config{Store: pstore, Orch: orch, Core: store, Approvals: approvals, Rec: rec, Runtime: rt, Guard: guard, Sink: deliverableSink{next: asvc, drive: dsvc, log: log}, OrgID: cfg.App.OrgID, MaxAttempts: max(1, cfg.App.TaskMaxAttempts), Log: log, Limits: cfg.Projects}
	// W5 monitor tuning (all optional): PROJECT_POLL_MS (active period),
	// PROJECT_POLL_IDLE_MAX_MS (idle backoff ceiling), PROJECT_BUDGET_WARN_PCT (1-100).
	if v, err := strconv.Atoi(os.Getenv("PROJECT_POLL_MS")); err == nil && v > 0 {
		pcfg.Poll = time.Duration(v) * time.Millisecond
	}
	if v, err := strconv.Atoi(os.Getenv("PROJECT_POLL_IDLE_MAX_MS")); err == nil && v > 0 {
		pcfg.PollIdleMax = time.Duration(v) * time.Millisecond
	}
	if v, err := strconv.ParseFloat(os.Getenv("PROJECT_BUDGET_WARN_PCT"), 64); err == nil && v >= 1 && v <= 100 {
		pcfg.BudgetWarnPct = v / 100
	}
	if modelPolicy != nil {
		// Q3: estimates are priced at the model each role is routed to.
		pcfg.ModelFor = func(ctx context.Context, role string) string { return modelPolicy.Runtime(ctx).RoleModels[role] }
	}
	pcfg.SynthTokenBudget, pcfg.SynthMaxGroups = cfg.App.SynthTokenBudget, cfg.App.SynthMaxGroups
	if orgCfg != nil {
		pcfg.Catalog = orgCfg.Catalog()
		pcfg.LocaleFor = func(ctx context.Context) string { return orgCfg.Locale(ctx, "") }
	}
	psvc = projects.New(ctx, pcfg)
	orch.SetContextSource(asvc) // W3: referenced artifacts reach project tasks as delimited data
	dws := driveWorkspace{drive: dsvc, pstore: pstore, store: store}
	if orgCfg != nil {
		dws.locale = func(ctx context.Context) string { return orgCfg.Locale(ctx, "") }
	}
	orch.SetFileResolver(driveFileResolver{drive: dsvc}) // chat: file cards and attachments
	orch.SetWorkspaceSource(dws)                         // F1: agents know their drive folders (and find their work kit)
	return workspaceWiring{pstore: pstore, projects: psvc, artifacts: asvc, drive: dsvc, sandbox: wireSandbox(log, pg, rec, cw, orch, dsvc), objects: objects, docIndex: docIdx}
}
