package main

import (
	"context"
	"os"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/inboxes"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// wireInboxes builds the data inboxes (docs/architecture/data-inboxes.md): with Postgres they live in migration
// 803, without it in memory (the open demo). Pushes land in the drive, so without a drive there are no inboxes.
func wireInboxes(cfg config.Config, pg *postgres.Store, rec *application.Recorder, ctl *controls.Service, cs *customers.Service, dr *drive.Service) *inboxes.Service {
	if dr == nil {
		return nil
	}
	c := inboxes.Config{Drive: dr, OrgID: cfg.App.OrgID, Gate: inboxGate(ctl),
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "data_inbox", EntityID: id, Details: d})
		},
		Emit: func(ctx context.Context, org, typ string, payload map[string]any) {
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, Entity: "data_inbox", Payload: payload, SkipAudit: true})
		}}
	if cs != nil {
		c.Customers = inboxCustomers{cs: cs}
	}
	if pg != nil {
		c.Store = &postgres.InboxStore{S: pg}
	} else {
		c.Store = inboxes.NewMemStore()
	}
	return inboxes.New(c)
}

// inboxGate refuses pushes while the organization's kill switch is set or it is in read-only mode. Unreadable
// controls refuse as read-only (fail closed). Without a controls service the gate is open.
func inboxGate(ctl *controls.Service) func(ctx context.Context, org string) error {
	if ctl == nil {
		return nil
	}
	return func(ctx context.Context, org string) error {
		st, err := ctl.State(ctx, org)
		switch {
		case err != nil:
			return inboxes.ErrReadOnly
		case st.KillSwitchLevel != "" && st.KillSwitchLevel != controls.LevelNone:
			return inboxes.ErrKillSwitch
		case st.Mode == controls.ModeReadOnly:
			return inboxes.ErrReadOnly
		}
		return nil
	}
}

// inboxCustomers resolves the drive folder of an active customer (B12). Archived customers give ErrConflict and
// unknown ones ErrNotFound, which the inbox service reports as customer_unavailable.
type inboxCustomers struct{ cs *customers.Service }

func (a inboxCustomers) CustomerFolder(ctx context.Context, org, customerID string) (string, error) {
	if _, err := a.cs.Active(ctx, org, customerID); err != nil {
		return "", err
	}
	c, err := a.cs.Get(application.WithOrg(ctx, org), customerID)
	if err != nil {
		return "", err
	}
	return c.FolderPath, nil
}

// inboxBaseURL is the public API base shown in the push URL: PUBLIC_API_URL (".../api/v1"), else UI_BASE_URL +
// "/api/v1" (single-domain deploys), else "" and the UI composes it from its own origin.
func inboxBaseURL(uiBase string) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_API_URL")), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(strings.TrimSpace(uiBase), "/"); v != "" {
		return v + "/api/v1"
	}
	return ""
}
