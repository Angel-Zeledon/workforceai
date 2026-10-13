package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/webtool"
)

// webRunner adapts the web tool service to the orchestrator's port.
type webRunner struct{ svc *webtool.Service }

func (w webRunner) Run(ctx context.Context, c application.WebCall) application.WebResult {
	o := w.svc.Execute(ctx, webtool.Exec{Org: c.Org, AgentID: c.AgentID, TaskID: c.TaskID, Action: c.Action, Args: c.Args})
	return application.WebResult{Status: o.Status, DenyReason: o.DenyReason, Summary: o.Summary, Block: o.Block, Notice: o.Notice,
		CostUSD: o.CostUSD, Tainted: o.Tainted, InjectionSuspected: o.InjectionSuspected}
}

// wireWeb builds the web tool (docs/architecture/agent-web.md). The service always exists (the setting can be
// read and edited); it is installed in the orchestrator, which still checks the agent grant, the kill switch,
// read-only mode and the policy before every call. The tool is OFF for every organization until an
// owner/admin enables it.
//
// Env: WEB_SEARCH_PROVIDER (brave|tavily|searxng; unset = search unavailable), WEB_SEARCH_API_KEY, WEB_SEARCH_URL
// (searxng), WEB_SEARCH_COST_USD (default 0.005), WEB_MAX_CALLS_PER_DAY (platform ceiling per organization,
// default 200), WEB_ALLOWED_HOSTS (optional operator allow-list, comma separated), WEB_ALLOWED_PORTS (default 80,443).
func wireWeb(log *slog.Logger, pg *postgres.Store, rec *application.Recorder, cw connWiring, orch *application.Orchestrator) *webtool.Service {
	var store webtool.Store = webtool.NewMemoryStore()
	if pg != nil {
		store = &postgres.WebStore{S: pg}
	}
	svc := &webtool.Service{Store: store, Suspects: cw.sus,
		CeilingPerDay: envPositive("WEB_MAX_CALLS_PER_DAY", 200), SearchCostUSD: 0.005,
		Audit: func(ctx context.Context, org, actor, action, entity, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: d})
		}}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("WEB_SEARCH_COST_USD")), 64); err == nil && v >= 0 {
		svc.SearchCostUSD = v
	}
	for _, h := range strings.Split(os.Getenv("WEB_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			if d, err := webtool.NormalizeDomain(h); err == nil {
				svc.OperatorHosts = append(svc.OperatorHosts, d)
			} else {
				log.Warn("WEB_ALLOWED_HOSTS: ignoring invalid entry", "entry", h)
			}
		}
	}
	guard := webtool.NetGuard{}
	for _, p := range strings.Split(os.Getenv("WEB_ALLOWED_PORTS"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 && n < 65536 {
			guard.Ports = append(guard.Ports, n)
		}
	}
	svc.Fetcher = &webtool.Fetcher{Guard: guard}
	prov, err := webtool.NewProvider(os.Getenv("WEB_SEARCH_PROVIDER"), os.Getenv("WEB_SEARCH_API_KEY"), os.Getenv("WEB_SEARCH_URL"), nil)
	switch {
	case err != nil:
		log.Warn("web search disabled: bad configuration", "err", err)
	case prov == nil:
		log.Info("WEB_SEARCH_PROVIDER not set: agents cannot search the web (web.fetch still works for URLs people give)")
	default:
		svc.Provider = prov
		log.Info("web search ready", "provider", prov.Name())
	}
	orch.SetWebRunner(webRunner{svc: svc})
	return svc
}
