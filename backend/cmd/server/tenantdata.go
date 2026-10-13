package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/tenantdata"
)

// wireTenantData builds the GDPR export / deletion service (on the RLS-restricted
// application pool) and starts the purge janitor (on the owner connection:
// MIGRATE_DATABASE_URL, else DATABASE_URL without the app role) every 15 minutes.
// Several replicas are safe (advisory lock). TENANT_JANITOR=off disables the
// janitor (deletions then stay pending). Nil without Postgres.
func wireTenantData(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, accounts auth.Store, limiter auth.Limiter, objects objstore.Store) *tenantdata.Service {
	if pg == nil {
		return nil
	}
	// Objects only matter when the bytes live outside Postgres (S3); drive_objects is exported and purged as a table.
	var external objstore.Store
	if _, inPG := objects.(*postgres.ObjectStore); objects != nil && !inPG {
		external = objects
	}
	svc := &tenantdata.Service{App: pg.Pool(), Limiter: limiter, Objects: external}
	if accounts != nil {
		svc.Accounts = accounts
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("TENANT_JANITOR"))); v == "off" || v == "false" || v == "0" {
		log.Warn("TENANT_JANITOR=off: scheduled organization deletions and expired trials are not purged")
		return svc
	}
	ownerURL := cfg.MigrateURL
	if ownerURL == "" {
		ownerURL = cfg.DatabaseURL
	}
	pc, err := pgxpool.ParseConfig(ownerURL)
	if err != nil {
		log.Error("tenantdata: owner connection", "err", err)
		return svc
	}
	pc.MaxConns = 3 // one holds the advisory lock, the others purge
	owner, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		log.Error("tenantdata: owner connection", "err", err)
		return svc
	}
	j := &tenantdata.Janitor{Pool: owner, Purger: &tenantdata.Purger{Owner: owner, Objects: external, Log: log}, Log: log}
	go func() {
		defer owner.Close()
		j.Run(ctx)
	}()
	return svc
}
