package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/slackapp"
	"aiworkforce/backend/internal/vault"
)

// wireSlack builds the two-way Slack channel for humans (docs/architecture/channels.md). Each organization brings
// its own Slack app; its secrets are sealed with the vault (without a vault key nothing can be stored). Without
// accounts (AUTH_ENABLED=false) there is nobody to map a Slack user to, so every decision is refused.
//
//	SLACK_CHANNEL=off   do not mount the channel at all
func wireSlack(log *slog.Logger, cfg config.Config, pg *postgres.Store, store application.Store, approvals *application.Approvals,
	authStore auth.Store, ctl *controls.Service, v *vault.Vault, rec *application.Recorder, obs *slackapp.Observer) *slackapp.Service {
	if s := strings.ToLower(strings.TrimSpace(os.Getenv("SLACK_CHANNEL"))); s == "off" || s == "false" || s == "0" {
		log.Info("Slack channel disabled (SLACK_CHANNEL=off)")
		return nil
	}
	var st slackapp.Store = slackapp.NewMemStore()
	if pg != nil {
		st = &slackapp.PG{DB: pg}
	}
	svc := &slackapp.Service{Store: st, Slack: &slackapp.Client{}, Log: log,
		Approvals: slackapp.ApprovalsAdapter{Approvals: approvals, Store: store},
		PublicURL: strings.TrimRight(firstNonEmpty(os.Getenv("NOTIFY_PUBLIC_URL"), cfg.UIBaseURL), "/"),
		AppURL:    strings.TrimRight(cfg.UIBaseURL, "/"),
		AgentName: func(ctx context.Context, org, id string) string {
			if a, err := store.GetAgent(ctx, org, id); err == nil {
				return a.Name
			}
			return ""
		},
		Audit: func(ctx context.Context, org string, e domain.AuditLog) {
			rec.Audit(application.WithOrg(ctx, org), e)
		},
	}
	if v != nil && v.Enabled() {
		svc.Sealer = v
	} else {
		log.Info("Slack channel: no vault key, Slack apps cannot be connected (the secrets are only stored sealed)")
	}
	if authStore != nil {
		svc.Directory = slackDirectory{st: authStore}
	}
	if ctl != nil {
		svc.Controls = func(ctx context.Context, org string) (string, bool) {
			s, err := ctl.State(ctx, org)
			if err != nil {
				return "unknown", false // fail closed: approvals from Slack are refused when the state cannot be read
			}
			return s.KillSwitchLevel, s.Mode == controls.ModeReadOnly
		}
	}
	svc.Status = func(ctx context.Context, org string) (slackapp.OfficeStatus, error) {
		var out slackapp.OfficeStatus
		aps, err := store.ListApprovals(ctx, org, string(domain.ApprovalPending))
		if err != nil {
			return out, err
		}
		running, err := store.ListTasks(ctx, org, "", string(domain.TaskRunning))
		if err != nil {
			return out, err
		}
		waiting, err := store.ListTasks(ctx, org, "", string(domain.TaskAwaitingApproval))
		if err != nil {
			return out, err
		}
		out.PendingApprovals, out.Running, out.Awaiting = len(aps), len(running), len(waiting)
		if svc.Controls != nil {
			out.KillSwitch, out.ReadOnly = svc.Controls(ctx, org)
		}
		return out, nil
	}
	obs.Bind(svc)
	return svc
}

// slackDirectory maps platform users for the Slack channel with the same source of truth as the web session:
// the membership row read now (a removed or demoted person loses Slack decisions immediately).
type slackDirectory struct{ st auth.Store }

func (d slackDirectory) Member(ctx context.Context, org, userID string) (slackapp.Member, error) {
	m, err := d.st.GetMembership(ctx, org, userID)
	if err != nil {
		return slackapp.Member{}, slackapp.ErrNotFound
	}
	u, err := d.st.UserByID(ctx, userID)
	if err != nil {
		return slackapp.Member{}, slackapp.ErrNotFound
	}
	if !u.TrialExpiresAt.IsZero() && time.Now().After(u.TrialExpiresAt) {
		return slackapp.Member{}, slackapp.ErrNotFound
	}
	return slackapp.Member{UserID: u.ID, Email: u.Email, Name: u.Name, Role: string(m.Role)}, nil
}

func (d slackDirectory) MemberByVerifiedEmail(ctx context.Context, org, email string) (slackapp.Member, error) {
	u, err := d.st.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			return slackapp.Member{}, slackapp.ErrNotFound
		}
		return slackapp.Member{}, err
	}
	if u.VerifiedAt.IsZero() {
		return slackapp.Member{}, slackapp.ErrNotFound // an address nobody proved on the platform never maps
	}
	return d.Member(ctx, org, u.ID)
}
