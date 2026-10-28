package slackapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// ---- inbound (public; authenticated by the Slack signature) ----
//
//	POST /api/v1/channels/slack/{org}/interactions   Interactivity request URL (button presses)
//	POST /api/v1/channels/slack/{org}/commands       Slash command request URL
//
// The organization comes from the URL the admin configured in Slack; its signing secret must verify the request and
// the workspace in the payload must be the one of its bot token. Anything else is answered without detail.

// readVerified reads the capped body and verifies the Slack signature with the organization's signing secret.
// It writes the error answer itself and returns ok=false when the request must not be processed.
func (s *Service) readVerified(w http.ResponseWriter, r *http.Request, org string) (Record, string, []byte, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return Record{}, "", nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return Record{}, "", nil, false
	}
	rec, found, err := s.Store.GetRecord(r.Context(), org)
	if err != nil || !found || org == "" {
		http.NotFound(w, r)
		return Record{}, "", nil, false
	}
	signing, bot := s.secrets(r.Context(), rec)
	if signing == "" {
		http.NotFound(w, r)
		return Record{}, "", nil, false
	}
	if err := Verify(signing, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, s.now()); err != nil {
		s.log().Warn("slackapp: request refused", "org", org, "reason", err.Error())
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return Record{}, "", nil, false
	}
	return rec, bot, body, true
}

// interaction is the part of a block_actions payload the channel reads.
type interaction struct {
	Type string `json:"type"`
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	User struct {
		ID     string `json:"id"`
		TeamID string `json:"team_id"`
	} `json:"user"`
	ResponseURL string `json:"response_url"`
	Actions     []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
		ActionTS string `json:"action_ts"`
	} `json:"actions"`
}

// DedupeKey identifies one button press: Slack sends the same action_ts when it retries a delivery, and a double
// click produces two presses of the same person on the same approval within the same second at most once each.
func DedupeKey(team, user, action, value, actionTS string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{team, user, action, value, actionTS}, "\x00")))
	return hex.EncodeToString(h[:])
}

// InteractionsHandler serves the Interactivity request URL. orgOf reads {org} from the path.
func (s *Service) InteractionsHandler(orgOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := orgOf(r)
		rec, bot, body, ok := s.readVerified(w, r, org)
		if !ok {
			return
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var p interaction
		if err := json.Unmarshal([]byte(form.Get("payload")), &p); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		team := p.Team.ID
		if team == "" {
			team = p.User.TeamID
		}
		if rec.TeamID == "" || team != rec.TeamID {
			s.log().Warn("slackapp: interaction from another workspace", "org", org)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK) // Slack wants the ack within 3 s; the work continues in the background
		if p.Type != "block_actions" || len(p.Actions) == 0 {
			return
		}
		a := p.Actions[0]
		if a.ActionID != ActionApprove && a.ActionID != ActionReject {
			return // "open in the app" is a plain link
		}
		key := DedupeKey(team, p.User.ID, a.ActionID, a.Value, a.ActionTS)
		claimed, err := s.Store.Claim(r.Context(), org, key, s.now())
		if err != nil {
			s.log().Warn("slackapp: claim", "org", org, "err", err)
			return
		}
		if !claimed {
			return // a retry or a double click: the first press already decided and answered
		}
		ctx := context.WithoutCancel(r.Context())
		s.spawn(func() {
			c, cancel := context.WithTimeout(ctx, interactionTimeout)
			defer cancel()
			d := s.Decide(c, org, rec, bot, team, p.User.ID, a.ActionID, a.Value)
			if err := s.Store.SetOutcome(c, org, key, d.Outcome); err != nil {
				s.log().Warn("slackapp: outcome", "org", org, "err", err)
			}
			if p.ResponseURL != "" {
				if err := s.Slack.Respond(c, p.ResponseURL, d.Reply); err != nil {
					s.log().Warn("slackapp: respond", "org", org, "err", slackCode(err))
				}
			}
		})
	})
}

// CommandsHandler serves the slash command request URL. The answer is ephemeral (only the person sees it).
func (s *Service) CommandsHandler(orgOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := orgOf(r)
		rec, bot, body, ok := s.readVerified(w, r, org)
		if !ok {
			return
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		team := form.Get("team_id")
		if rec.TeamID == "" || team != rec.TeamID {
			s.log().Warn("slackapp: command from another workspace", "org", org)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
		defer cancel()
		out := s.Command(ctx, org, rec, bot, team, form.Get("user_id"), form.Get("command"), form.Get("text"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Command answers one slash command.
func (s *Service) Command(ctx context.Context, org string, rec Record, bot, team, slackUser, command, txt string) map[string]any {
	loc := rec.Locale
	if command == "" || !strings.HasPrefix(command, "/") || len(command) > 40 {
		command = DefaultCommand
	}
	if !rec.Enabled {
		return ephemeral(tr(loc, "rDisabled"))
	}
	fields := strings.Fields(txt)
	sub := ""
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}
	switch sub {
	case "vincular", "link":
		if len(fields) < 2 {
			return ephemeral(fmt.Sprintf(tr(loc, "linkUsage"), command))
		}
		return s.link(ctx, org, rec, team, slackUser, fields[1])
	case "desvincular", "unlink":
		l, err := s.Store.GetLink(ctx, org, team, slackUser)
		if err != nil {
			return ephemeral(tr(loc, "unlinkNone"))
		}
		if _, err := s.Unlink(ctx, org, l.UserID); err != nil {
			return ephemeral(tr(loc, "rFail"))
		}
		return ephemeral(tr(loc, "unlinkOK"))
	case "estado", "status":
		who, why := s.resolve(ctx, org, rec, bot, team, slackUser)
		if r := s.identityRefusal(loc, command, why); r != nil {
			return r
		}
		if !canRead(who.Member.Role) {
			return ephemeral(tr(loc, "rForbidden"))
		}
		return s.status(ctx, org, loc)
	case "pendientes", "pending", "pendentes":
		who, why := s.resolve(ctx, org, rec, bot, team, slackUser)
		if r := s.identityRefusal(loc, command, why); r != nil {
			return r
		}
		if !canRead(who.Member.Role) {
			return ephemeral(tr(loc, "rForbidden"))
		}
		return s.pending(ctx, org, rec, canDecide(who.Member.Role))
	}
	return ephemeral(fmt.Sprintf(tr(loc, "help"), command))
}

func (s *Service) identityRefusal(loc, command, why string) map[string]any {
	switch why {
	case "":
		return nil
	case OutcomeUnlinked:
		return ephemeral(fmt.Sprintf(tr(loc, "rUnlinked"), command))
	case OutcomeNotMember:
		return ephemeral(tr(loc, "rNotMember"))
	}
	return ephemeral(tr(loc, "rFail"))
}

// link consumes a one-time code issued in the app and links this Slack user to its owner.
func (s *Service) link(ctx context.Context, org string, rec Record, team, slackUser, code string) map[string]any {
	loc := rec.Locale
	brake := org + "\x00" + team + "\x00" + slackUser
	if slackUser == "" || s.Directory == nil || s.tooManyFailures(brake) {
		return ephemeral(tr(loc, "linkBad"))
	}
	userID, err := s.Store.ConsumeCode(ctx, org, hashCode(org, normCode(code)), s.now())
	if err != nil {
		s.fail(brake)
		s.audit(ctx, org, domain.AuditLog{Actor: "slack:" + team + ":" + slackUser, Action: "channel.slack.link_refused", Entity: "slack_user", EntityID: slackUser,
			Details: map[string]any{"channel": "slack", "slack_team": team, "reason": "invalid_code"}})
		return ephemeral(tr(loc, "linkBad"))
	}
	m, err := s.Directory.Member(ctx, org, userID)
	if err != nil {
		return ephemeral(tr(loc, "rNotMember"))
	}
	if err := s.Store.PutLink(ctx, Link{OrgID: org, TeamID: team, SlackUserID: slackUser, UserID: m.UserID, Method: LinkByCode, CreatedAt: s.now()}); err != nil {
		return ephemeral(tr(loc, "rFail"))
	}
	s.audit(ctx, org, domain.AuditLog{Actor: m.UserID, Action: "channel.slack.linked", Entity: "user", EntityID: m.UserID,
		Details: map[string]any{"channel": "slack", "slack_team": team, "slack_user": slackUser, "method": LinkByCode}})
	name := m.Email
	if name == "" {
		name = m.Name
	}
	return ephemeral(fmt.Sprintf(tr(loc, "linkOK"), esc(name)))
}

func (s *Service) status(ctx context.Context, org, loc string) map[string]any {
	if s.Status == nil {
		return ephemeral(tr(loc, "rFail"))
	}
	st, err := s.Status(ctx, org)
	if err != nil {
		return ephemeral(tr(loc, "rFail"))
	}
	lines := []string{
		fmt.Sprintf(tr(loc, "stPending"), st.PendingApprovals),
		fmt.Sprintf(tr(loc, "stRunning"), st.Running),
		fmt.Sprintf(tr(loc, "stAwaiting"), st.Awaiting),
	}
	switch {
	case st.KillSwitch != "" && st.KillSwitch != "none":
		lines = append(lines, ":octagonal_sign: "+fmt.Sprintf(tr(loc, "stKill"), esc(st.KillSwitch)))
	case st.ReadOnly:
		lines = append(lines, ":lock: "+tr(loc, "stRO"))
	default:
		lines = append(lines, tr(loc, "stNormal"))
	}
	title := "*" + tr(loc, "statusTitle") + "*"
	out := ephemeral(stripMarks(title) + "\n" + strings.Join(lines, "\n"))
	out["blocks"] = []map[string]any{{"type": "section", "text": mrkdwn(title + "\n" + strings.Join(lines, "\n"))}}
	return out
}

func (s *Service) pending(ctx context.Context, org string, rec Record, decide bool) map[string]any {
	loc := rec.Locale
	list, err := s.Approvals.Pending(ctx, org)
	if err != nil {
		return ephemeral(tr(loc, "rFail"))
	}
	if len(list) == 0 {
		return ephemeral(tr(loc, "pNone"))
	}
	blocks := []map[string]any{{"type": "section", "text": mrkdwn("*" + fmt.Sprintf(tr(loc, "pTitle"), len(list)) + "*")}}
	if !decide {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{mrkdwn(tr(loc, "pViewOnly"))}})
	}
	for i, ap := range list {
		if i == maxPendingListed {
			blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{mrkdwn(fmt.Sprintf(tr(loc, "pMore"), len(list)-i))}})
			break
		}
		c := card{Locale: loc, Approval: ap, Agent: s.agent(ctx, org, ap), OpenURL: s.openURL(ap.ID), NeedsApp: needsApp(rec, ap)}
		m := c.msg()
		if !decide {
			// Without the permission the buttons are not offered (the server would refuse them anyway).
			m.Blocks = m.Blocks[:len(m.Blocks)-1]
			if c.OpenURL != "" {
				m.Blocks = append(m.Blocks, map[string]any{"type": "actions", "elements": []map[string]any{
					{"type": "button", "action_id": ActionOpen, "text": plain(tr(loc, "open")), "url": c.OpenURL}}})
			}
		}
		blocks = append(blocks, map[string]any{"type": "divider"})
		blocks = append(blocks, m.Blocks...)
	}
	out := ephemeral(fmt.Sprintf(tr(loc, "pTitle"), len(list)))
	out["blocks"] = blocks
	return out
}

// ---- authenticated API (mounted by the API layer with permissions) ----

// API groups the handlers of the settings card and of the account link. Org and Actor come from the verified
// request context of the API layer, never from client input.
type API struct {
	Svc   *Service
	Org   func(*http.Request) string
	Actor func(*http.Request) string
	Fail  func(w http.ResponseWriter, err error)
	Write func(w http.ResponseWriter, status int, v any)
}

func (a API) fail(w http.ResponseWriter, err error) {
	var se *SlackAuthError
	switch {
	case errors.Is(err, ErrVaultRequired):
		a.Write(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "vault_required"})
	case errors.Is(err, ErrNotConfigured):
		a.Write(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "not_configured"})
	case errors.As(err, &se):
		a.Write(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "slack_auth_failed", "slack_error": se.Code})
	default:
		a.Fail(w, err)
	}
}

// GetSettings: GET /channels/slack/settings.
func (a API) GetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := a.Svc.Settings(r.Context(), a.Org(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, v)
}

// PutSettings: PUT /channels/slack/settings.
func (a API) PutSettings(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		a.fail(w, errors.Join(domain.ErrInvalid, err))
		return
	}
	v, err := a.Svc.Save(r.Context(), a.Org(r), a.Actor(r), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, v)
}

// DeleteSettings: DELETE /channels/slack/settings (disconnect: secrets and links are removed).
func (a API) DeleteSettings(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.Disconnect(r.Context(), a.Org(r), a.Actor(r)); err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, map[string]any{"disconnected": true})
}

// Test: POST /channels/slack/test.
func (a API) Test(w http.ResponseWriter, r *http.Request) {
	res, err := a.Svc.Test(r.Context(), a.Org(r), a.Actor(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, res)
}

// MyLink: GET /channels/slack/link - the Slack accounts linked to the caller.
func (a API) MyLink(w http.ResponseWriter, r *http.Request) {
	links, err := a.Svc.LinksOf(r.Context(), a.Org(r), a.Actor(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, map[string]any{"linked": len(links) > 0, "links": links})
}

// LinkCode: POST /channels/slack/link-code - a one-time code to type in Slack (`/oficina vincular <code>`).
func (a API) LinkCode(w http.ResponseWriter, r *http.Request) {
	code, exp, err := a.Svc.IssueCode(r.Context(), a.Org(r), a.Actor(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.Write(w, 200, map[string]any{"code": code, "expires_at": exp, "command": DefaultCommand + " vincular " + code})
}

// Unlink: DELETE /channels/slack/link - removes the caller's Slack links.
func (a API) Unlink(w http.ResponseWriter, r *http.Request) {
	n, err := a.Svc.Unlink(r.Context(), a.Org(r), a.Actor(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Write(w, 200, map[string]any{"removed": n})
}
