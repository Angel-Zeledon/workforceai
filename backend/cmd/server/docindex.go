package main

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// Passage index of the drive (capability 6, docs/architecture/agent-search.md).
// newDocIndex builds it next to the drive (Postgres or memory); bindDocIndex
// adds the embeddings provider, the data policy, the budget and the locale once
// those exist, and starts the background worker.

func newDocIndex(log *slog.Logger, pg *postgres.Store, dsvc *drive.Service, orgID string) *docindex.Service {
	var st docindex.Store = docindex.NewMemStore(dsvc)
	if pg != nil {
		st = &postgres.DocIndexStore{S: pg}
	}
	return docindex.New(docindex.Config{Store: st, Drive: dsvc, OrgID: orgID, Limits: docindex.LimitsFromEnv(), Log: log})
}

// docSpend makes embedding calls count against the organization budget: a
// reservation against the caps before the call, a usage-ledger entry after.
type docSpend struct {
	budget *application.Budget
	store  application.Store
}

func (d docSpend) Allow(ctx context.Context, org string, usd float64) bool {
	if d.budget == nil {
		return true
	}
	res, ex, err := d.budget.Reserve(application.WithOrg(ctx, org), "", "", usd)
	if err != nil || ex != nil {
		return false
	}
	res.Release()
	return true
}

func (d docSpend) Record(ctx context.Context, org string, tokens int, usd float64, model string) {
	if d.store == nil || usd <= 0 {
		return
	}
	// InputTokens stays 0 so nothing re-prices the call at a chat model's rate;
	// the tokens are in doc_embed_usage (daily cap).
	_ = d.store.AddUsage(application.WithOrg(context.WithoutCancel(ctx), org), org, domain.UsageEntry{ID: "emb_" + uuid.NewString(), AgentID: "system",
		Kind: domain.UsageKind("embed"), Model: "embeddings/" + model, CostUSD: usd, Tools: []string{"fs.search"}, TS: time.Now().UTC()})
}

func bindDocIndex(ctx context.Context, log *slog.Logger, doc *docindex.Service, store application.Store, rec *application.Recorder, orch *application.Orchestrator,
	modelPolicy *application.ModelPolicyService, llmSvc *application.LLMService, orgCfg *application.OrgConfig) {
	if doc == nil {
		return
	}
	rc := docindex.ResolverConfig{Platform: docindex.EmbedConfigFromEnv()}
	if modelPolicy != nil {
		rc.Allowed = func(c context.Context, org, provider string) bool {
			allowed := modelPolicy.Runtime(application.WithOrg(c, org)).AllowedProviders
			return len(allowed) == 0 || slices.Contains(allowed, provider)
		}
	}
	if llmSvc != nil {
		rc.OrgCredential = func(c context.Context, org string) (string, string, string, string, string, bool) {
			cr, byo := llmSvc.ActiveCredential(application.WithOrg(c, org))
			return cr.Provider, cr.APIKey, cr.Model, cr.BaseURL, cr.OrgID, byo
		}
	}
	b := docindex.Binding{Embeddings: docindex.NewResolver(rc), Spend: docSpend{budget: orch.Budget(), store: store},
		Audit: func(c context.Context, org, actor, action string, details map[string]any) {
			rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: actor, Action: action, Entity: "org", EntityID: org, Details: details})
		}}
	if orgCfg != nil {
		b.Locale = func(c context.Context, org string) string { return orgCfg.Locale(application.WithOrg(c, org), "") }
	}
	if l, ok := store.(application.OrgLister); ok {
		b.Orgs = l.ListOrgIDs
	}
	doc.Bind(b)
	doc.Start(ctx)
	if rc.Platform.Provider == "" {
		log.Info("drive passage index ready (keyword search; set EMBEDDINGS_PROVIDER/EMBEDDINGS_API_KEY for search by meaning)")
	} else {
		log.Info("drive passage index ready", "embeddings_provider", rc.Platform.Provider, "embeddings_model", rc.Platform.Model)
	}
}
