package main

import (
	"aiworkforce/backend/internal/agenthistory"
	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// wireAgentHistory builds the configuration history of the agents (docs/architecture/agent-history.md): Postgres
// (migration 827) when there is a database, memory otherwise (the demo without one). Its sources read the services
// of the API, so it is built from the finished dependencies; the event observer is bound to it here.
func wireAgentHistory(pg *postgres.Store, deps api.Deps, obs *agenthistory.Observer) *agenthistory.Service {
	var st agenthistory.Store = agenthistory.NewMemStore()
	if pg != nil {
		st = &postgres.AgentHistoryStore{S: pg}
	}
	h := api.NewAgentHistory(deps, st)
	obs.Bind(h)
	return h
}
