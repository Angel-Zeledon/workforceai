package slackapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Sealer encrypts the two secrets under the organization's data key. *vault.Vault implements it.
type Sealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

// Seal purposes (bound as AAD: a sealed signing secret cannot be opened as a bot token, nor in another organization).
const (
	sealSigning = "slackapp-signing-secret"
	sealBot     = "slackapp-bot-token"
)

// Member is a platform user of the organization with the role they hold NOW.
type Member struct {
	UserID string
	Email  string
	Name   string
	Role   string // owner | admin | member | viewer
}

// Directory resolves platform users. Both methods return ErrNotFound when the person is not (or no longer) a member
// of org, or the account cannot act (for example an expired trial).
type Directory interface {
	Member(ctx context.Context, org, userID string) (Member, error)
	// MemberByVerifiedEmail finds the member whose e-mail is this one AND was verified on the platform.
	MemberByVerifiedEmail(ctx context.Context, org, email string) (Member, error)
}

// Approvals is what the channel needs from the approvals of the app. Decide must apply exactly the rules of
// POST /approvals/{id}/decision for that member (ApprovalsAdapter does).
type Approvals interface {
	Get(ctx context.Context, org, id string) (domain.Approval, error)
	Pending(ctx context.Context, org string) ([]domain.Approval, error)
	Decide(ctx context.Context, org, id, decision, note string, by Member) (domain.Approval, error)
}

// OfficeStatus is the answer of "/oficina estado".
type OfficeStatus struct {
	PendingApprovals int
	Running          int
	Awaiting         int
	KillSwitch       string // "" or "none" when off
	ReadOnly         bool
}

// Errors of the settings API (the HTTP layer maps them to stable codes).
var (
	ErrVaultRequired = errors.New("slackapp: the secrets can only be stored sealed with the vault, and this deployment has no vault key")
	ErrNotConfigured = errors.New("slackapp: the Slack app is not configured")
)

// SlackAuthError: Slack refused the bot token (auth.test).
type SlackAuthError struct{ Code string }

func (e *SlackAuthError) Error() string { return "slackapp: Slack refused the bot token: " + e.Code }

// Interaction outcomes (audited and stored with the dedupe key).
const (
	OutcomeProcessing  = "processing"
	OutcomeApproved    = "approved"
	OutcomeRejected    = "rejected"
	OutcomePartial     = "partial"
	OutcomeUnlinked    = "unlinked"
	OutcomeNotMember   = "not_member"
	OutcomeForbidden   = "forbidden"
	OutcomeRoleTooLow  = "role_too_low"
	OutcomeSelf        = "self_approval"
	OutcomeAlready     = "already_approved_by_you"
	OutcomeNotPending  = "not_pending"
	OutcomeKillSwitch  = "kill_switch"
	OutcomeReadOnly    = "read_only"
	OutcomeNeedsApp    = "needs_app"
	OutcomeDisabled    = "disabled"
	OutcomeNotFound    = "not_found"
	OutcomeFailed      = "failed"
	DefaultCodeTTL     = 10 * time.Minute
	maxLinkFailures    = 5
	linkFailureWindow  = 15 * time.Minute
	maxPendingListed   = 5
	interactionTimeout = 30 * time.Second
)

// Service is the Slack channel of one deployment.
type Service struct {
	Store     Store
	Sealer    Sealer // required to store secrets (ErrVaultRequired otherwise)
	Slack     *Client
	Directory Directory // nil (accounts off): nobody can be mapped, every decision is refused
	Approvals Approvals
	// Controls returns the kill-switch level ("" / "none" = off) and whether the organization is in read-only mode.
	Controls func(ctx context.Context, org string) (kill string, readOnly bool)
	Status   func(ctx context.Context, org string) (OfficeStatus, error)
	// AgentName (optional) turns an agent id into a name for the message.
	AgentName func(ctx context.Context, org, id string) string
	// Audit records an entry in the organization's audit trail.
	Audit func(ctx context.Context, org string, e domain.AuditLog)
	// PublicURL is the public base of the API (the request URLs shown to the admin); AppURL is where the app lives.
	PublicURL string
	AppURL    string
	CodeTTL   time.Duration
	Log       *slog.Logger
	Now       func() time.Time
	// Go runs background work (default: a goroutine). Tests run it inline.
	Go func(func())

	failMu   sync.Mutex
	failures map[string][]time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) spawn(fn func()) {
	if s.Go != nil {
		s.Go(fn)
		return
	}
	go fn()
}

func (s *Service) audit(ctx context.Context, org string, e domain.AuditLog) {
	if s.Audit != nil {
		s.Audit(ctx, org, e)
	}
}

func (s *Service) codeTTL() time.Duration {
	if s.CodeTTL > 0 {
		return s.CodeTTL
	}
	return DefaultCodeTTL
}

func (s *Service) vaultOn() bool { return s.Sealer != nil && s.Sealer.Enabled() }

func (s *Service) appURL() string { return strings.TrimRight(s.AppURL, "/") }

func (s *Service) openURL(id string) string {
	if s.appURL() == "" {
		return ""
	}
	return s.appURL() + "/approvals/" + url.PathEscape(id)
}

// secrets opens the two secrets of a record ("" when missing or unopenable; failures are logged without the secret).
func (s *Service) secrets(ctx context.Context, r Record) (signing, bot string) {
	if !s.vaultOn() {
		return "", ""
	}
	if len(r.SigningSecretEnc) > 0 {
		if b, err := s.Sealer.Unseal(ctx, r.OrgID, sealSigning, r.SigningSecretEnc); err == nil {
			signing = string(b)
		} else {
			s.log().Warn("slackapp: signing secret cannot be opened", "org", r.OrgID)
		}
	}
	if len(r.BotTokenEnc) > 0 {
		if b, err := s.Sealer.Unseal(ctx, r.OrgID, sealBot, r.BotTokenEnc); err == nil {
			bot = string(b)
		} else {
			s.log().Warn("slackapp: bot token cannot be opened", "org", r.OrgID)
		}
	}
	return signing, bot
}

// ---- settings ----

// View is what the settings API returns. The secrets never leave the server.
type View struct {
	Enabled          bool       `json:"enabled"`
	Configured       bool       `json:"configured"`
	SigningSecretSet bool       `json:"signing_secret_set"`
	BotTokenSet      bool       `json:"bot_token_set"`
	BotTokenLast4    string     `json:"bot_token_last4,omitempty"`
	TeamID           string     `json:"team_id,omitempty"`
	TeamName         string     `json:"team_name,omitempty"`
	ChannelID        string     `json:"channel_id"`
	MinRisk          string     `json:"min_risk"`
	Locale           string     `json:"locale"`
	EmailMatching    bool       `json:"email_matching"`
	HighRiskInApp    bool       `json:"high_risk_in_app"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	UpdatedBy        string     `json:"updated_by,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
	// Where to point the Slack app (Interactivity & Shortcuts / Slash Commands). Relative paths when the
	// deployment does not know its public address.
	InteractionsURL string `json:"interactions_url"`
	CommandsURL     string `json:"commands_url"`
	Command         string `json:"command"`
	// Scopes the bot token needs.
	Scopes            []string `json:"scopes"`
	VaultAvailable    bool     `json:"vault_available"`
	AccountsAvailable bool     `json:"accounts_available"`
}

// Input is a change of the settings. nil keeps the stored value; an empty secret keeps it too (use Disconnect to
// remove the app).
type Input struct {
	Enabled       *bool   `json:"enabled"`
	SigningSecret *string `json:"signing_secret"`
	BotToken      *string `json:"bot_token"`
	ChannelID     *string `json:"channel_id"`
	MinRisk       *string `json:"min_risk"`
	Locale        *string `json:"locale"`
	EmailMatching *bool   `json:"email_matching"`
	HighRiskInApp *bool   `json:"high_risk_in_app"`
}

// RequiredScopes are the bot token scopes the channel uses.
var RequiredScopes = []string{"chat:write", "commands", "users:read", "users:read.email"}

var (
	reSigning = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)
	reBot     = regexp.MustCompile(`^xoxb-[A-Za-z0-9-]{10,250}$`)
	reChannel = regexp.MustCompile(`^[CGD][A-Z0-9]{6,20}$`)
)

// RequestPaths returns the two inbound paths of an organization (relative to the API base).
func RequestPaths(org string) (interactions, commands string) {
	base := "/api/v1/channels/slack/" + url.PathEscape(org)
	return base + "/interactions", base + "/commands"
}

func (s *Service) view(r Record) View {
	in, cmd := RequestPaths(r.OrgID)
	if base := strings.TrimRight(s.PublicURL, "/"); base != "" {
		in, cmd = base+in, base+cmd
	}
	v := View{Enabled: r.Enabled, SigningSecretSet: len(r.SigningSecretEnc) > 0, BotTokenSet: len(r.BotTokenEnc) > 0,
		BotTokenLast4: r.TokenLast4, TeamID: r.TeamID, TeamName: r.TeamName, ChannelID: r.ChannelID, MinRisk: r.MinRisk,
		Locale: r.Locale, EmailMatching: r.EmailMatching, HighRiskInApp: r.HighRiskInApp, VerifiedAt: r.VerifiedAt, UpdatedBy: r.UpdatedBy,
		InteractionsURL: in, CommandsURL: cmd, Command: DefaultCommand, Scopes: RequiredScopes,
		VaultAvailable: s.vaultOn(), AccountsAvailable: s.Directory != nil}
	v.Configured = v.SigningSecretSet && v.BotTokenSet && r.TeamID != ""
	if !r.UpdatedAt.IsZero() {
		t := r.UpdatedAt
		v.UpdatedAt = &t
	}
	return v
}

// Settings returns the view of the organization's Slack app.
func (s *Service) Settings(ctx context.Context, org string) (View, error) {
	r, _, err := s.Store.GetRecord(ctx, org)
	if err != nil {
		return View{}, err
	}
	r.OrgID = org
	return s.view(r), nil
}

// Save applies a change. A new bot token is checked with Slack (auth.test) before it is stored, which also fixes the
// workspace whose requests are accepted. Secrets are stored only sealed.
func (s *Service) Save(ctx context.Context, org, actor string, in Input) (View, error) {
	r, _, err := s.Store.GetRecord(ctx, org)
	if err != nil {
		return View{}, err
	}
	r.OrgID = org
	changed := []string{}
	newSigning := in.SigningSecret != nil && strings.TrimSpace(*in.SigningSecret) != ""
	newBot := in.BotToken != nil && strings.TrimSpace(*in.BotToken) != ""
	if (newSigning || newBot) && !s.vaultOn() {
		return View{}, ErrVaultRequired
	}
	if newSigning {
		v := strings.TrimSpace(*in.SigningSecret)
		if !reSigning.MatchString(v) {
			return View{}, fmt.Errorf("%w: the signing secret must be the value shown in Basic Information of the Slack app", domain.ErrInvalid)
		}
		enc, err := s.Sealer.Seal(ctx, org, sealSigning, []byte(v))
		if err != nil {
			return View{}, fmt.Errorf("slackapp: sealing the signing secret: %w", err)
		}
		r.SigningSecretEnc = enc
		changed = append(changed, "signing_secret")
	}
	if newBot {
		v := strings.TrimSpace(*in.BotToken)
		if !reBot.MatchString(v) {
			return View{}, fmt.Errorf("%w: the bot token must start with xoxb-", domain.ErrInvalid)
		}
		info, err := s.Slack.AuthTest(ctx, v)
		if err != nil {
			code := "unreachable"
			var ae *APIError
			if errors.As(err, &ae) {
				code = ae.Code
			}
			return View{}, &SlackAuthError{Code: code}
		}
		enc, err := s.Sealer.Seal(ctx, org, sealBot, []byte(v))
		if err != nil {
			return View{}, fmt.Errorf("slackapp: sealing the bot token: %w", err)
		}
		now := s.now()
		r.BotTokenEnc, r.TokenLast4 = enc, v[len(v)-4:]
		r.TeamID, r.TeamName, r.BotUserID, r.VerifiedAt = info.TeamID, clip(oneLine(info.Team), 80), info.UserID, &now
		changed = append(changed, "bot_token")
	}
	if in.ChannelID != nil {
		v := strings.ToUpper(strings.TrimSpace(*in.ChannelID))
		if v != "" && !reChannel.MatchString(v) {
			return View{}, fmt.Errorf("%w: channel_id must be a Slack channel id such as C0123456789 (not its name)", domain.ErrInvalid)
		}
		r.ChannelID = v
		changed = append(changed, "channel_id")
	}
	if in.MinRisk != nil {
		v := strings.ToLower(strings.TrimSpace(*in.MinRisk))
		if v != "low" && v != "medium" && v != "high" {
			return View{}, fmt.Errorf("%w: min_risk must be low, medium or high", domain.ErrInvalid)
		}
		r.MinRisk = v
		changed = append(changed, "min_risk")
	}
	if in.Locale != nil {
		v := strings.ToLower(strings.TrimSpace(*in.Locale))
		if v != "es" && v != "en" && v != "pt" {
			return View{}, fmt.Errorf("%w: locale must be es, en or pt", domain.ErrInvalid)
		}
		r.Locale = v
		changed = append(changed, "locale")
	}
	if in.EmailMatching != nil {
		r.EmailMatching = *in.EmailMatching
		changed = append(changed, "email_matching")
	}
	if in.HighRiskInApp != nil {
		r.HighRiskInApp = *in.HighRiskInApp
		changed = append(changed, "high_risk_in_app")
	}
	if in.Enabled != nil {
		if *in.Enabled && (len(r.SigningSecretEnc) == 0 || len(r.BotTokenEnc) == 0 || r.TeamID == "") {
			return View{}, fmt.Errorf("%w: paste the signing secret and the bot token before turning the channel on", domain.ErrInvalid)
		}
		r.Enabled = *in.Enabled
		changed = append(changed, "enabled")
	}
	r.UpdatedBy, r.UpdatedAt = actor, s.now()
	if err := s.Store.PutRecord(ctx, r); err != nil {
		return View{}, err
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "channel.slack.settings_updated", Entity: "slack_app", EntityID: org,
		Details: map[string]any{"channel": "slack", "changed": changed, "enabled": r.Enabled, "team_id": r.TeamID, "bot_token_last4": r.TokenLast4,
			"channel_id": r.ChannelID, "email_matching": r.EmailMatching, "high_risk_in_app": r.HighRiskInApp}})
	return s.view(r), nil
}

// Disconnect removes the app, its secrets and every user link of the organization.
func (s *Service) Disconnect(ctx context.Context, org, actor string) error {
	if err := s.Store.DeleteAll(ctx, org); err != nil {
		return err
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "channel.slack.disconnected", Entity: "slack_app", EntityID: org,
		Details: map[string]any{"channel": "slack"}})
	return nil
}

// TestResult is the answer of POST /channels/slack/test.
type TestResult struct {
	OK       bool   `json:"ok"`
	TeamID   string `json:"team_id,omitempty"`
	TeamName string `json:"team_name,omitempty"`
	Posted   bool   `json:"posted"`
	Error    string `json:"error,omitempty"` // Slack's code (invalid_auth, channel_not_found, not_in_channel...)
}

// Test checks the bot token again and, when a channel is set, posts a harmless message there.
func (s *Service) Test(ctx context.Context, org, actor string) (TestResult, error) {
	r, found, err := s.Store.GetRecord(ctx, org)
	if err != nil {
		return TestResult{}, err
	}
	if !found {
		return TestResult{}, ErrNotConfigured
	}
	_, bot := s.secrets(ctx, r)
	if bot == "" {
		return TestResult{}, ErrNotConfigured
	}
	res := TestResult{}
	info, err := s.Slack.AuthTest(ctx, bot)
	if err != nil {
		res.Error = slackCode(err)
	} else {
		res.OK, res.TeamID, res.TeamName = true, info.TeamID, info.Team
		if info.TeamID != r.TeamID || info.UserID != r.BotUserID {
			now := s.now()
			r.TeamID, r.TeamName, r.BotUserID, r.VerifiedAt = info.TeamID, clip(oneLine(info.Team), 80), info.UserID, &now
			if err := s.Store.PutRecord(ctx, r); err != nil {
				return res, err
			}
		}
		if r.ChannelID != "" {
			if _, _, err := s.Slack.PostMessage(ctx, bot, r.ChannelID, Msg{Text: tr(r.Locale, "test")}); err != nil {
				res.Error = slackCode(err)
			} else {
				res.Posted = true
			}
		}
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "channel.slack.tested", Entity: "slack_app", EntityID: org,
		Details: map[string]any{"channel": "slack", "ok": res.OK, "posted": res.Posted, "error": res.Error}})
	return res, nil
}

func slackCode(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return "unreachable"
}

// ---- account links ----

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I

func hashCode(org, code string) string {
	h := sha256.Sum256([]byte("slackapp-link:" + org + ":" + code))
	return hex.EncodeToString(h[:])
}

func normCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
}

// IssueCode creates a one-time code that links the Slack account which types it to userID. A new code replaces the
// previous one of the same person. Only its hash is stored.
func (s *Service) IssueCode(ctx context.Context, org, userID string) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, fmt.Errorf("%w: no user", domain.ErrForbidden)
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	raw := make([]byte, 8)
	for i := range b {
		raw[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	code := string(raw)
	exp := s.now().Add(s.codeTTL())
	if err := s.Store.CreateCode(ctx, org, hashCode(org, code), userID, exp); err != nil {
		return "", time.Time{}, err
	}
	s.audit(ctx, org, domain.AuditLog{Actor: userID, Action: "channel.slack.link_code_issued", Entity: "user", EntityID: userID,
		Details: map[string]any{"channel": "slack", "expires_at": exp}})
	return code[:4] + "-" + code[4:], exp, nil
}

// LinksOf lists the Slack accounts linked to userID.
func (s *Service) LinksOf(ctx context.Context, org, userID string) ([]Link, error) {
	return s.Store.LinksOf(ctx, org, userID)
}

// Unlink removes every Slack link of userID.
func (s *Service) Unlink(ctx context.Context, org, userID string) (int, error) {
	n, err := s.Store.DeleteLinksOf(ctx, org, userID)
	if err == nil && n > 0 {
		s.audit(ctx, org, domain.AuditLog{Actor: userID, Action: "channel.slack.unlinked", Entity: "user", EntityID: userID,
			Details: map[string]any{"channel": "slack", "links": n}})
	}
	return n, err
}

// tooManyFailures is a small brake on guessing link codes (per Slack user of a workspace).
func (s *Service) tooManyFailures(key string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	cut := s.now().Add(-linkFailureWindow)
	keep := s.failures[key][:0]
	for _, t := range s.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if s.failures == nil {
		s.failures = map[string][]time.Time{}
	}
	s.failures[key] = keep
	return len(keep) >= maxLinkFailures
}

func (s *Service) fail(key string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if s.failures == nil {
		s.failures = map[string][]time.Time{}
	}
	s.failures[key] = append(s.failures[key], s.now())
}

// ---- identity ----

// identity is the result of mapping a Slack user to a platform member.
type identity struct {
	Member Member
	Method string // code | email
}

// resolve maps (team, Slack user) to a platform member. The Slack payload's identity is only an index: the person
// must have linked the account with a one-time code issued in the app, or (when the organization allows it) have
// the same e-mail confirmed in Slack AND verified on the platform. The membership role is read now, every time.
func (s *Service) resolve(ctx context.Context, org string, r Record, bot, team, slackUser string) (identity, string) {
	if s.Directory == nil || team == "" || slackUser == "" {
		return identity{}, OutcomeUnlinked
	}
	var id identity
	if l, err := s.Store.GetLink(ctx, org, team, slackUser); err == nil {
		m, err := s.Directory.Member(ctx, org, l.UserID)
		if err != nil {
			return identity{}, OutcomeNotMember
		}
		return identity{Member: m, Method: LinkByCode}, ""
	} else if !errors.Is(err, ErrNotFound) {
		s.log().Warn("slackapp: link lookup", "org", org, "err", err)
		return identity{}, OutcomeFailed
	}
	if !r.EmailMatching || bot == "" {
		return identity{}, OutcomeUnlinked
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u, err := s.Slack.UserInfo(cctx, bot, slackUser)
	if err != nil || u.Deleted || u.IsBot || !u.IsEmailConfirmed || u.Email == "" || (u.TeamID != "" && u.TeamID != team) {
		return identity{}, OutcomeUnlinked
	}
	m, err := s.Directory.MemberByVerifiedEmail(ctx, org, u.Email)
	if err != nil {
		return identity{}, OutcomeUnlinked
	}
	id = identity{Member: m, Method: LinkByEmail}
	return id, ""
}

// canDecide: the same permission the web route POST /approvals/{id}/decision requires.
func canDecide(role string) bool { return auth.Role(role).Has(auth.PermApprovalsDecide) }

func canRead(role string) bool { return auth.Role(role).Has(auth.PermApprovalsRead) }

// needsApp: approving this from Slack is not allowed (high risk when the organization keeps those in the app, and
// code execution always).
func needsApp(r Record, ap domain.Approval) bool {
	if strings.Contains(strings.ToLower(ap.Action), "code.run") || strings.EqualFold(ap.Tool, application.CodeToolName) {
		return true
	}
	return r.HighRiskInApp && riskRank(ap.Risk) >= 3
}

func riskRank(r string) int {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high", "critical":
		return 3
	}
	return 2
}

// ---- decisions ----

// decision is the outcome of one button press.
type decision struct {
	Outcome string
	Reply   map[string]any // body for the response_url (replace the original or reply ephemerally)
	Member  Member
	Method  string
}

// Decide applies a button press of slackUser on approval id. Every path ends in an audited outcome.
func (s *Service) Decide(ctx context.Context, org string, r Record, bot, team, slackUser, action, id string) decision {
	loc := r.Locale
	verb := "approve"
	if action == ActionReject {
		verb = "reject"
	}
	d := s.decide(ctx, org, r, bot, team, slackUser, verb, id)
	actor := d.Member.UserID
	if actor == "" {
		actor = "slack:" + team + ":" + slackUser
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "channel.slack.decision", Entity: "approval", EntityID: id,
		Details: map[string]any{"channel": "slack", "decision": verb, "outcome": d.Outcome, "slack_team": team, "slack_user": slackUser,
			"identity": d.Method}})
	if d.Reply == nil {
		d.Reply = ephemeral(tr(loc, "rFail"))
	}
	return d
}

func (s *Service) refusal(loc, key string, args ...any) map[string]any {
	t := tr(loc, key)
	if len(args) > 0 {
		t = fmt.Sprintf(t, args...)
	}
	return ephemeral(t)
}

func (s *Service) decide(ctx context.Context, org string, r Record, bot, team, slackUser, verb, id string) decision {
	loc := r.Locale
	if !r.Enabled {
		return decision{Outcome: OutcomeDisabled, Reply: s.refusal(loc, "rDisabled")}
	}
	who, why := s.resolve(ctx, org, r, bot, team, slackUser)
	switch why {
	case "":
	case OutcomeUnlinked:
		return decision{Outcome: why, Reply: s.refusal(loc, "rUnlinked", DefaultCommand)}
	case OutcomeNotMember:
		return decision{Outcome: why, Reply: s.refusal(loc, "rNotMember")}
	default:
		return decision{Outcome: OutcomeFailed, Reply: s.refusal(loc, "rFail")}
	}
	d := decision{Member: who.Member, Method: who.Method}
	if !canDecide(who.Member.Role) {
		d.Outcome, d.Reply = OutcomeForbidden, s.refusal(loc, "rForbidden")
		return d
	}
	ap, err := s.Approvals.Get(ctx, org, id)
	if err != nil {
		d.Outcome, d.Reply = OutcomeNotFound, s.refusal(loc, "rNotFound")
		return d
	}
	if ap.Status != domain.ApprovalPending {
		d.Outcome = OutcomeNotPending
		d.Reply = replace(card{Locale: loc, Approval: ap, Agent: s.agent(ctx, org, ap), OpenURL: s.openURL(ap.ID), Status: closedLine(loc, ap)}.msg())
		return d
	}
	if verb == "approve" {
		if s.Controls != nil {
			kill, ro := s.Controls(ctx, org)
			if kill != "" && kill != "none" {
				d.Outcome, d.Reply = OutcomeKillSwitch, s.refusal(loc, "rKill")
				return d
			}
			if ro {
				d.Outcome, d.Reply = OutcomeReadOnly, s.refusal(loc, "rReadOnly")
				return d
			}
		}
		if needsApp(r, ap) {
			d.Outcome, d.Reply = OutcomeNeedsApp, s.refusal(loc, "needsApp")
			return d
		}
	}
	out, err := s.Approvals.Decide(withSlackUser(ctx, slackUser), org, id, verb, "decided from Slack", who.Member)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrForbidden):
			switch {
			case policy.RoleRank(who.Member.Role) < policy.RoleRank(ap.RequiredRole):
				d.Outcome, d.Reply = OutcomeRoleTooLow, s.refusal(loc, "rRole")
			case ap.NoSelfApproval && ap.RequestedBy != "" && ap.RequestedBy == who.Member.UserID:
				d.Outcome, d.Reply = OutcomeSelf, s.refusal(loc, "rSelf")
			default:
				d.Outcome, d.Reply = OutcomeForbidden, s.refusal(loc, "rForbidden")
			}
		case errors.Is(err, domain.ErrConflict):
			for _, dec := range ap.Decisions {
				if dec.By == who.Member.UserID {
					d.Outcome, d.Reply = OutcomeAlready, s.refusal(loc, "rAlready")
					return d
				}
			}
			cur, gerr := s.Approvals.Get(ctx, org, id)
			if gerr != nil {
				cur = ap
			}
			d.Outcome = OutcomeNotPending
			d.Reply = replace(card{Locale: loc, Approval: cur, Agent: s.agent(ctx, org, cur), OpenURL: s.openURL(cur.ID), Status: closedLine(loc, cur)}.msg())
		default:
			s.log().Warn("slackapp: decide", "org", org, "approval", id, "err", err)
			d.Outcome, d.Reply = OutcomeFailed, s.refusal(loc, "rFail")
		}
		return d
	}
	c := card{Locale: loc, Approval: out, Agent: s.agent(ctx, org, out), OpenURL: s.openURL(out.ID), NeedsApp: needsApp(r, out)}
	switch out.Status {
	case domain.ApprovalApproved:
		d.Outcome, c.Status = OutcomeApproved, statusLine(loc, out, mention(slackUser))
	case domain.ApprovalRejected:
		d.Outcome, c.Status = OutcomeRejected, statusLine(loc, out, mention(slackUser))
	default:
		d.Outcome, c.Partial = OutcomePartial, partialLine(loc, out, slackUser)
	}
	d.Reply = replace(c.msg())
	return d
}

func partialLine(loc string, ap domain.Approval, slackUser string) string {
	line := fmt.Sprintf(tr(loc, "stPartial"), len(ap.Decisions), max(ap.RequiredApprovals, 1))
	if slackUser != "" {
		line += " " + mention(slackUser)
	}
	return line
}

func replace(m Msg) map[string]any {
	return map[string]any{"replace_original": true, "text": m.Text, "blocks": m.Blocks}
}

func (s *Service) agent(ctx context.Context, org string, ap domain.Approval) string {
	if s.AgentName == nil || ap.AgentID == "" {
		return ""
	}
	return s.AgentName(ctx, org, ap.AgentID)
}

// ---- outbound: post approval requests, keep them up to date ----

type slackUserKey struct{}

func withSlackUser(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, slackUserKey{}, u)
}

func slackUserFrom(ctx context.Context) string {
	u, _ := ctx.Value(slackUserKey{}).(string)
	return u
}

// Post sends a pending approval to the organization's channel (when the app is on, a channel is set and the risk
// reaches min_risk) and remembers the message so it can be updated when the approval moves.
func (s *Service) Post(ctx context.Context, org string, ap domain.Approval) {
	if ap.ID == "" || ap.Status != domain.ApprovalPending {
		return
	}
	r, found, err := s.Store.GetRecord(ctx, org)
	if err != nil || !found || !r.Enabled || r.ChannelID == "" || riskRank(ap.Risk) < riskRank(r.MinRisk) {
		return
	}
	_, bot := s.secrets(ctx, r)
	if bot == "" {
		return
	}
	m := card{Locale: r.Locale, Approval: ap, Agent: s.agent(ctx, org, ap), OpenURL: s.openURL(ap.ID), NeedsApp: needsApp(r, ap)}.msg()
	ch, ts, err := s.Slack.PostMessage(ctx, bot, r.ChannelID, m)
	if err != nil {
		s.log().Warn("slackapp: post approval", "org", org, "approval", ap.ID, "err", slackCode(err))
		return
	}
	if err := s.Store.PutMessage(ctx, org, ap.ID, ch, ts); err != nil {
		s.log().Warn("slackapp: remember message", "org", org, "err", err)
	}
}

// Refresh updates the posted message of an approval after progress or resolution (decided in the app, by a link,
// by Slack or by a timeout). A decision made from Slack carries the Slack user so the message says who decided.
func (s *Service) Refresh(ctx context.Context, org string, ap domain.Approval) {
	ch, ts, err := s.Store.GetMessage(ctx, org, ap.ID)
	if err != nil {
		return
	}
	r, found, err := s.Store.GetRecord(ctx, org)
	if err != nil || !found {
		return
	}
	_, bot := s.secrets(ctx, r)
	if bot == "" {
		return
	}
	su := slackUserFrom(ctx)
	c := card{Locale: r.Locale, Approval: ap, Agent: s.agent(ctx, org, ap), OpenURL: s.openURL(ap.ID), NeedsApp: needsApp(r, ap)}
	switch {
	case ap.Status == domain.ApprovalPending:
		c.Partial = partialLine(r.Locale, ap, su)
	case su != "":
		c.Status = statusLine(r.Locale, ap, mention(su))
	default:
		c.Status = closedLine(r.Locale, ap)
	}
	if err := s.Slack.Update(ctx, bot, ch, ts, c.msg()); err != nil {
		s.log().Warn("slackapp: update approval message", "org", org, "approval", ap.ID, "err", slackCode(err))
	}
}

// Publisher is the event port (same shape as application.Publisher).
type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

// Observer decorates the event publisher like notify.Observer: events pass through unchanged and first; approval
// requests are posted and progress / resolution update the posted message, in the background.
type Observer struct {
	Inner Publisher
	mu    sync.RWMutex
	svc   *Service
}

// Bind installs the service (built after the publisher).
func (o *Observer) Bind(s *Service) {
	o.mu.Lock()
	o.svc = s
	o.mu.Unlock()
}

// Publish implements Publisher.
func (o *Observer) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	o.mu.RLock()
	svc := o.svc
	o.mu.RUnlock()
	if svc == nil {
		return err
	}
	ap, ok := approvalOf(e.Payload)
	if !ok {
		return err
	}
	var fn func(context.Context, string, domain.Approval)
	switch e.Type {
	case domain.EvApprovalRequest:
		fn = svc.Post
	case domain.EvApprovalProgress, domain.EvApprovalResolved:
		fn = svc.Refresh
	default:
		return err
	}
	org := e.OrgID
	svc.spawn(func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				svc.log().Error("slackapp: panic", "panic", r)
			}
		}()
		fn(c, org, ap)
	})
	return err
}

func approvalOf(payload any) (domain.Approval, bool) {
	if p, ok := payload.(map[string]any); ok {
		switch a := p["approval"].(type) {
		case domain.Approval:
			return a, a.ID != ""
		case *domain.Approval:
			if a != nil {
				return *a, a.ID != ""
			}
		}
	}
	return domain.Approval{}, false
}

// ---- adapter to the approvals of the app ----

// ApprovalsAdapter implements Approvals on application.Approvals. Unlike a signed link (which counts as an admin
// within strict limits), a Slack decision carries the mapped person's own id and CURRENT role, so every rule of the
// web applies: required role, double approval with distinct people, no self-approval, agents never decide.
type ApprovalsAdapter struct {
	Approvals *application.Approvals
	Store     application.Store
}

// Get implements Approvals.
func (a ApprovalsAdapter) Get(ctx context.Context, org, id string) (domain.Approval, error) {
	return a.Store.GetApproval(ctx, org, id)
}

// Pending implements Approvals.
func (a ApprovalsAdapter) Pending(ctx context.Context, org string) ([]domain.Approval, error) {
	return a.Store.ListApprovals(ctx, org, string(domain.ApprovalPending))
}

// Decide implements Approvals.
func (a ApprovalsAdapter) Decide(ctx context.Context, org, id, decision, note string, by Member) (domain.Approval, error) {
	// Never let an empty role fall back to the demo default (owner) of application.ActorRoleFrom.
	if by.UserID == "" || !auth.Role(by.Role).Valid() {
		return domain.Approval{}, fmt.Errorf("%w: no mapped user or role", domain.ErrForbidden)
	}
	ctx = application.WithOrg(ctx, org)
	ctx = application.WithActor(ctx, by.UserID)
	ctx = application.WithActorRole(ctx, by.Role)
	ctx = application.WithChannel(ctx, "slack")
	return a.Approvals.Decide(ctx, id, decision, note)
}
