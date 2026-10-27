package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/linkimports"
	"aiworkforce/backend/internal/webtool"
)

// wireLinkImports builds "import from a link" (docs/architecture/link-imports.md) and starts its schedule (the
// rituals pattern: an in-process ticker, organization by organization). With Postgres the imports live in
// migration 804; without it, in memory. The download reuses the agents' web tool guard; CUSTOM_API_ALLOW_PRIVATE
// (the same switch as the generic API sources) lets a self-hosted install reach its LAN and LINK_IMPORTS_ALLOW_HTTP
// accepts plain http links. The auth header is sealed with the connections vault (no KEK: no header). Read-only
// mode and the kill switch skip runs. Without a drive the feature is off.
func wireLinkImports(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, rec *application.Recorder, cw connWiring,
	dsvc *drive.Service, cust *customers.Service, lister application.OrgLister) *linkimports.Service {
	if dsvc == nil {
		return nil
	}
	var st linkimports.Store = linkimports.NewMemStore()
	if pg != nil {
		st = &postgres.LinkImportStore{S: pg}
	}
	allowPrivate := envBool("CUSTOM_API_ALLOW_PRIVATE")
	allowHTTP := envBool("LINK_IMPORTS_ALLOW_HTTP")
	if allowHTTP {
		log.Warn("LINK_IMPORTS_ALLOW_HTTP=true: link imports may download over plain http (the link and its auth header travel unencrypted)")
	}
	lc := linkimports.Config{Store: st, Drive: dsvc, OrgID: cfg.App.OrgID, Log: log,
		Fetcher: &linkimports.Fetcher{Guard: webtool.NetGuard{Ports: linkimports.Ports}, AllowPrivate: allowPrivate, AllowHTTP: allowHTTP},
		Gate:    linkImportGate(cw.ctl),
		Audit: func(c context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: actor, Action: action, Entity: "link_import", EntityID: id, Details: d})
		},
		Emit: func(c context.Context, org, typ string, p map[string]any) {
			rec.Emit(application.WithOrg(c, org), application.Action{Type: typ, Entity: "link_import", EntityID: fmt.Sprint(p["import_id"]), Payload: p, SkipAudit: true})
		},
	}
	if cw.vault != nil {
		lc.Sealer = cw.vault
	}
	if cust != nil {
		lc.Customers = linkImportCustomers{svc: cust}
	}
	svc := linkimports.New(lc)
	svc.Start(ctx, time.Minute, lister)
	return svc
}

func envBool(name string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return v
}

// linkImportGate maps the controls state: kill switch first, then read-only; an unreadable state refuses as
// read-only (fail closed).
func linkImportGate(ctl *controls.Service) func(ctx context.Context, org string) error {
	if ctl == nil {
		return nil
	}
	return func(ctx context.Context, org string) error {
		st, err := ctl.State(ctx, org)
		switch {
		case err != nil:
			return linkimports.ErrReadOnly
		case st.KillSwitchLevel != "" && st.KillSwitchLevel != controls.LevelNone:
			return linkimports.ErrKillSwitch
		case st.Mode == controls.ModeReadOnly:
			return linkimports.ErrReadOnly
		}
		return nil
	}
}

// linkImportCustomers resolves a customer's drive folder (B12): unknown -> not found, archived -> conflict.
type linkImportCustomers struct{ svc *customers.Service }

func (c linkImportCustomers) CustomerFolder(ctx context.Context, org, id string) (string, error) {
	cu, err := c.svc.Get(application.WithOrg(ctx, org), id)
	if err != nil {
		return "", err
	}
	if cu.Archived {
		return "", domain.ErrConflict
	}
	return cu.FolderPath, nil
}

// orgLister is the store's organization listing when it has one (nil: only the default organization).
func orgLister(store application.Store) application.OrgLister {
	l, _ := store.(application.OrgLister)
	return l
}
