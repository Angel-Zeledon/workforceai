package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/integrations"
	"aiworkforce/backend/internal/slackapp"
)

// Two-way Slack channel for humans (docs/architecture/channels.md, internal/slackapp).
//
//	POST   /channels/slack/{org}/interactions  public  Slack Interactivity request URL (signed by Slack)
//	POST   /channels/slack/{org}/commands      public  Slack slash command request URL (signed by Slack)
//	GET    /channels/slack/settings            org:manage      the organization's Slack app (secrets never returned)
//	PUT    /channels/slack/settings            org:manage      paste credentials / change options (audited)
//	DELETE /channels/slack/settings            org:manage      disconnect: secrets and links removed (audited)
//	POST   /channels/slack/test                org:manage      auth.test + test message to the channel
//	GET    /channels/slack/link                approvals:read  the caller's linked Slack accounts
//	POST   /channels/slack/link-code           approvals:read  one-time code to type in Slack
//	DELETE /channels/slack/link                approvals:read  unlink the caller
//
// v1: the channel is a vendor-specific integration. When VENDOR_INTEGRATIONS does not enable "slack", the public
// request URLs are not mounted (404) and the settings endpoints answer that the channel is disabled
// (GET /channels/slack/settings -> 200 {available:false, code}; every other route -> 403 vendor_integrations_disabled).
func (s *server) mountSlackPublic(r chi.Router) {
	if s.Slack == nil || !s.vendorEnabled(integrations.SlackChannel) {
		return
	}
	orgOf := func(r *http.Request) string { return chi.URLParam(r, "org") }
	r.Handle("/channels/slack/{org}/interactions", s.Slack.InteractionsHandler(orgOf))
	r.Handle("/channels/slack/{org}/commands", s.Slack.CommandsHandler(orgOf))
}

func (s *server) mountSlack(r chi.Router) {
	if !s.vendorEnabled(integrations.SlackChannel) {
		s.mountSlackDisabled(r)
		return
	}
	if s.Slack == nil {
		return
	}
	a := slackapp.API{Svc: s.Slack, Org: s.org, Actor: s.actor, Fail: s.fail, Write: writeJSON}
	manage := s.can(auth.PermOrgManage)
	r.With(manage).Get("/channels/slack/settings", a.GetSettings)
	r.With(manage).Put("/channels/slack/settings", a.PutSettings)
	r.With(manage).Delete("/channels/slack/settings", a.DeleteSettings)
	r.With(manage).Post("/channels/slack/test", a.Test)
	read := s.can(auth.PermApprovalsRead)
	r.With(read).Get("/channels/slack/link", a.MyLink)
	r.With(read).Post("/channels/slack/link-code", a.LinkCode)
	r.With(read).Delete("/channels/slack/link", a.Unlink)
}

// vendorEnabled reports whether a vendor-specific integration is on (nil policy = on: package tests).
func (s *server) vendorEnabled(id string) bool {
	return s.VendorIntegrations == nil || s.VendorIntegrations.Enabled(id)
}

// vendorState is what GET /org/settings reports as vendor_integrations.
func (s *server) vendorState() integrations.State {
	if s.VendorIntegrations == nil {
		return integrations.All().State()
	}
	return s.VendorIntegrations.State()
}

// mountSlackDisabled answers the Slack settings routes while the channel is switched off (v1).
func (s *server) mountSlackDisabled(r chi.Router) {
	disabled := func(w http.ResponseWriter, _ *http.Request) {
		codeErr(w, http.StatusForbidden, integrations.CodeDisabled, "the Slack channel is disabled in this deployment (VENDOR_INTEGRATIONS)")
	}
	manage := s.can(auth.PermOrgManage)
	r.With(manage).Get("/channels/slack/settings", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "enabled": false, "configured": false, "code": integrations.CodeDisabled})
	})
	r.With(manage).Put("/channels/slack/settings", disabled)
	r.With(manage).Delete("/channels/slack/settings", disabled)
	r.With(manage).Post("/channels/slack/test", disabled)
	read := s.can(auth.PermApprovalsRead)
	r.With(read).Get("/channels/slack/link", disabled)
	r.With(read).Post("/channels/slack/link-code", disabled)
	r.With(read).Delete("/channels/slack/link", disabled)
}
