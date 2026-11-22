package main

import (
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/officetemplates"
)

// wireOfficeTemplates keeps the office templates imported into each organization (F3,
// docs/architecture/office-templates.md): Postgres (migration 826, RLS) when there is a database, memory otherwise.
// Exporting and installing reuse the services already wired (agents, departments, profiles, skills, work templates,
// schedules and goals) through the API layer.
func wireOfficeTemplates(pg *postgres.Store) *officetemplates.Library {
	var st officetemplates.Store = officetemplates.NewMemStore()
	if pg != nil {
		st = &postgres.OfficeTemplateStore{S: pg}
	}
	return &officetemplates.Library{Store: st}
}
