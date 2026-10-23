package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
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
func (s *server) mountSlackPublic(r chi.Router) {
	if s.Slack == nil {
		return
	}
	orgOf := func(r *http.Request) string { return chi.URLParam(r, "org") }
	r.Handle("/channels/slack/{org}/interactions", s.Slack.InteractionsHandler(orgOf))
	r.Handle("/channels/slack/{org}/commands", s.Slack.CommandsHandler(orgOf))
}

func (s *server) mountSlack(r chi.Router) {
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
