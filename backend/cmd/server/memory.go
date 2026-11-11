package main

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/memory"
)

// startMemoryBackfill indexes what existed before the memory did. It runs AFTER the offices are wired
// (officeRecall): a document written before the office scope is installed would be filed as organization memory.
func startMemoryBackfill(ctx context.Context, log *slog.Logger, svc *memory.Service, store application.Store, orgIDs []string, defaultOrg string) {
	go func() { // idempotent, bounded, never blocks the start
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if defaultOrg != "" && !slices.Contains(orgIDs, defaultOrg) {
			orgIDs = append(orgIDs, defaultOrg)
		}
		for _, org := range orgIDs {
			backfillOrg(bctx, log, svc, store, org)
		}
	}()
}

// backfillOrg seals and re-indexes the memory of one organization. D3 (I4): first, with no cap, the organization
// memory the agents of private offices wrote before D3 moves to their office; then the re-index of the newest work
// (bounded), which also takes the older mixed reports out of the organization memory.
func backfillOrg(ctx context.Context, log *slog.Logger, svc *memory.Service, store memory.BackfillSource, org string) {
	octx := application.WithOrg(ctx, org)
	n, err := svc.SealOfficeMemory(octx, org)
	if err != nil {
		log.Warn("memory: seal office memory", "org", org, "err", err)
	}
	if n > 0 {
		log.Info("memory sealed into offices", "org", org, "documents", n)
	}
	if n := svc.Backfill(octx, org, store, 2000, 500); n > 0 {
		log.Info("memory backfill", "org", org, "documents", n)
	}
}

// wireMemory builds the long-term memory (docs/architecture/memory.md): the
// document index (Postgres or memory), the full-text retriever over documents
// and the drive files an agent may read, the memory.search tool of the Tool
// Gateway and the backfill of what happened before the memory existed.
func wireMemory(ctx context.Context, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	orch *application.Orchestrator, cw connWiring, ws workspaceWiring, orgIDs []string, defaultOrg string) *memory.Service {
	var docs memory.DocStore = memory.NewMemStore()
	if pg != nil {
		docs = &postgres.MemoryStore{S: pg}
	}
	fts := &memory.FTS{Docs: docs}
	if ws.drive != nil {
		fts.Files = &memory.DriveFiles{FS: &drive.AgentFS{Svc: ws.drive, Projects: projectDirectory{store: ws.pstore}, Customers: customerDirectory(ws.customers)}}
	}
	retrievers := []memory.Retriever{fts}
	var embedder memory.Embedder
	if vs, ok := docs.(memory.VecStore); ok && ws.docIndex != nil {
		// Search by meaning: the same embedder, data policy and daily token cap
		// as the drive passage index (EMBEDDINGS_*); without a provider the
		// semantic leg returns nothing and the memory stays keyword only.
		embedder = ws.docIndex
		retrievers = append(retrievers, &memory.Semantic{Docs: docs, Vecs: vs, Embedder: embedder, Suspects: cw.sus})
	}
	cfg := memory.Config{Docs: docs, Embedder: embedder, Retriever: &memory.Multi{Retrievers: retrievers}, Suspects: cw.sus, Log: log,
		CustomerOf: customerOf(ws.customers), // B12: a customer-scoped task recalls only that customer's work
		Audit: func(actx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(actx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "memory", EntityID: id, Details: d})
		}}
	if ws.pstore != nil {
		cfg.ProjectOf = func(actx context.Context, org, requestID string) (string, string) {
			r, err := ws.pstore.ByRequest(application.WithOrg(actx, org), org, requestID)
			if err != nil {
				return "", ""
			}
			return r.ID, r.Name
		}
	}
	if ws.drive != nil {
		cfg.ProjectPath = func(actx context.Context, org, projectID string) string {
			p, _ := ws.drive.ProjectFolder(application.WithOrg(actx, org), projectID, "", false)
			return p
		}
	}
	svc := memory.New(cfg)
	if cw.gw != nil {
		cw.gw.AddNative(&memory.GatewayTool{Svc: svc, Suspects: cw.sus, CustomerOf: customerOf(ws.customers)})
	}
	orch.SetMemorySource(svc)
	log.Info("long-term memory ready", "tool", memory.ToolName, "store", map[bool]string{true: "postgres", false: "memory"}[pg != nil])
	return svc
}
