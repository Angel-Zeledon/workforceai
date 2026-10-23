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
	go func() { // backfill: idempotent, bounded, never blocks the start
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if defaultOrg != "" && !slices.Contains(orgIDs, defaultOrg) {
			orgIDs = append(orgIDs, defaultOrg)
		}
		for _, org := range orgIDs {
			if n := svc.Backfill(application.WithOrg(bctx, org), org, store, 2000, 500); n > 0 {
				log.Info("memory backfill", "org", org, "documents", n)
			}
		}
	}()
	log.Info("long-term memory ready", "tool", memory.ToolName, "store", map[bool]string{true: "postgres", false: "memory"}[pg != nil])
	return svc
}
