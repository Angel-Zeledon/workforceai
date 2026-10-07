package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/controls"
)

// ConnStore implements connections.Store and controls.Store on Postgres
// (migrations 240 and 241). Every call runs through WithOrgTx, so Row-Level
// Security applies. The credentials table is NOT touched here: only the vault
// repository (vault_repo.go) reads or writes it.
type ConnStore struct {
	S *Store
	// ExtraOrgs are organizations to visit in background sweeps in addition to
	// those with memberships (the fixed demo org has none when auth is off).
	ExtraOrgs []string
}

var (
	_ connections.Store = (*ConnStore)(nil)
	_ controls.Store    = (*ConnStore)(nil)
)

func nf(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return connections.ErrNotFound
	}
	return err
}

// ---- connections ----

const connCols = `id, org_id, provider, kind, label, account_label, account_ref, mode, status, status_reason, class,
	requested_capabilities, granted_capabilities, provider_scopes, resource_scope, limits, read_only,
	last_tested_at, last_test_status, last_used_at, last_error_code, expires_at, created_by, created_at, updated_at,
	revoked_at, revoked_by, pending_provider_revocation, oauth_client_id, oauth_client_secret_enc`

func scanConn(r scanner) (connections.Connection, error) {
	var c connections.Connection
	var rs, lim []byte
	err := r.Scan(&c.ID, &c.OrgID, &c.Provider, &c.Kind, &c.Label, &c.AccountLabel, &c.AccountRef, &c.Mode, &c.Status, &c.StatusReason, &c.Class,
		&c.RequestedCapabilities, &c.GrantedCapabilities, &c.ProviderScopes, &rs, &lim, &c.ReadOnly,
		&c.LastTestedAt, &c.LastTestStatus, &c.LastUsedAt, &c.LastErrorCode, &c.ExpiresAt, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
		&c.RevokedAt, &c.RevokedBy, &c.PendingProviderRevocation, &c.OAuthClientID, &c.OAuthClientSecretEnc)
	unmarshal(rs, &c.ResourceScope)
	unmarshal(lim, &c.Limits)
	return c, err
}

func (c *ConnStore) CreateConnection(ctx context.Context, x connections.Connection) error {
	return c.S.exec(ctx, x.OrgID, `INSERT INTO connections (id, org_id, provider, kind, label, account_label, account_ref, mode, status, status_reason, class,
		requested_capabilities, granted_capabilities, provider_scopes, resource_scope, limits, read_only, last_tested_at, last_test_status,
		last_used_at, last_error_code, expires_at, created_by, created_at, updated_at, revoked_at, revoked_by, pending_provider_revocation,
		oauth_client_id, oauth_client_secret_enc)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)`,
		x.ID, x.OrgID, x.Provider, x.Kind, x.Label, x.AccountLabel, x.AccountRef, x.Mode, x.Status, x.StatusReason, x.Class,
		strs(x.RequestedCapabilities), strs(x.GrantedCapabilities), strs(x.ProviderScopes), jb(x.ResourceScope), jb(x.Limits), x.ReadOnly,
		x.LastTestedAt, x.LastTestStatus, x.LastUsedAt, x.LastErrorCode, x.ExpiresAt, x.CreatedBy, x.CreatedAt, x.UpdatedAt,
		x.RevokedAt, x.RevokedBy, x.PendingProviderRevocation, x.OAuthClientID, x.OAuthClientSecretEnc)
}

func (c *ConnStore) GetConnection(ctx context.Context, org, id string) (connections.Connection, error) {
	v, err := one(ctx, c.S, org, scanConn, `SELECT `+connCols+` FROM connections WHERE org_id=$1 AND id=$2`, org, id)
	return v, nf(err)
}

func (c *ConnStore) UpdateConnection(ctx context.Context, x connections.Connection) error {
	tag, err := c.S.execTag(ctx, x.OrgID, `UPDATE connections SET label=$3, account_label=$4, account_ref=$5, mode=$6, status=$7, status_reason=$8, class=$9,
		requested_capabilities=$10, granted_capabilities=$11, provider_scopes=$12, resource_scope=$13, limits=$14, read_only=$15,
		last_tested_at=$16, last_test_status=$17, last_used_at=$18, last_error_code=$19, expires_at=$20, updated_at=$21,
		revoked_at=$22, revoked_by=$23, pending_provider_revocation=$24, oauth_client_id=$25, oauth_client_secret_enc=$26
		WHERE org_id=$1 AND id=$2`,
		x.OrgID, x.ID, x.Label, x.AccountLabel, x.AccountRef, x.Mode, x.Status, x.StatusReason, x.Class,
		strs(x.RequestedCapabilities), strs(x.GrantedCapabilities), strs(x.ProviderScopes), jb(x.ResourceScope), jb(x.Limits), x.ReadOnly,
		x.LastTestedAt, x.LastTestStatus, x.LastUsedAt, x.LastErrorCode, x.ExpiresAt, x.UpdatedAt,
		x.RevokedAt, x.RevokedBy, x.PendingProviderRevocation, x.OAuthClientID, x.OAuthClientSecretEnc)
	if err == nil && tag.RowsAffected() == 0 {
		return connections.ErrNotFound
	}
	return err
}

func (c *ConnStore) ListConnections(ctx context.Context, org, provider, status string) ([]connections.Connection, error) {
	return many(ctx, c.S, org, scanConn, `SELECT `+connCols+` FROM connections WHERE org_id=$1
		AND ($2='' OR provider=$2) AND ($3='' OR status=$3) ORDER BY created_at`, org, provider, status)
}

// ---- OAuth state ----

func (c *ConnStore) PutOAuthState(ctx context.Context, s connections.OAuthState) error {
	return c.S.exec(ctx, s.OrgID, `INSERT INTO oauth_states (state_hash, org_id, connection_id, user_id, pkce_verifier_enc, redirect_uri, requested_capabilities, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, s.StateHash, s.OrgID, s.ConnectionID, s.UserID, s.VerifierEnc, s.RedirectURI, strs(s.Capabilities), s.ExpiresAt)
}

// ConsumeOAuthState atomically marks a state used. The callback has no
// organization yet: the RLS policy of oauth_states also admits the row whose
// hash equals app.oauth_state (a 256-bit unguessable value).
func (c *ConnStore) ConsumeOAuthState(ctx context.Context, hash string, now time.Time) (connections.OAuthState, error) {
	var s connections.OAuthState
	err := pgx.BeginFunc(ctx, c.S.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.oauth_state', $1, true)`, hash); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE oauth_states SET used_at=$2 WHERE state_hash=$1 AND used_at IS NULL AND expires_at > $2
			RETURNING state_hash, org_id, connection_id, user_id, pkce_verifier_enc, redirect_uri, requested_capabilities, expires_at`, hash, now).
			Scan(&s.StateHash, &s.OrgID, &s.ConnectionID, &s.UserID, &s.VerifierEnc, &s.RedirectURI, &s.Capabilities, &s.ExpiresAt)
	})
	return s, nf(err)
}

// ---- grants ----

const grantCols = `id, org_id, connection_id, agent_id, capabilities, resource_scope, constraints, max_risk, autonomy_override, limits,
	redaction_profile, is_default, alias, status, valid_from, valid_until, granted_by, requested_by, approved_by, created_at, revoked_at, revoked_by`

func scanGrant(r scanner) (connections.Grant, error) {
	var g connections.Grant
	var rs, cs, lim []byte
	err := r.Scan(&g.ID, &g.OrgID, &g.ConnectionID, &g.AgentID, &g.Capabilities, &rs, &cs, &g.MaxRisk, &g.AutonomyOverride, &lim,
		&g.RedactionProfile, &g.IsDefault, &g.Alias, &g.Status, &g.ValidFrom, &g.ValidUntil, &g.GrantedBy, &g.RequestedBy, &g.ApprovedBy, &g.CreatedAt, &g.RevokedAt, &g.RevokedBy)
	unmarshal(rs, &g.ResourceScope)
	unmarshal(cs, &g.Constraints)
	unmarshal(lim, &g.Limits)
	return g, err
}

func (c *ConnStore) UpsertGrant(ctx context.Context, g connections.Grant) error {
	return c.S.exec(ctx, g.OrgID, `INSERT INTO connection_grants (`+grantCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		ON CONFLICT (id) DO UPDATE SET capabilities=EXCLUDED.capabilities, resource_scope=EXCLUDED.resource_scope,
			constraints=EXCLUDED.constraints, max_risk=EXCLUDED.max_risk, autonomy_override=EXCLUDED.autonomy_override,
			limits=EXCLUDED.limits, redaction_profile=EXCLUDED.redaction_profile, is_default=EXCLUDED.is_default,
			alias=EXCLUDED.alias, status=EXCLUDED.status, valid_from=EXCLUDED.valid_from, valid_until=EXCLUDED.valid_until,
			granted_by=EXCLUDED.granted_by, requested_by=EXCLUDED.requested_by, approved_by=EXCLUDED.approved_by, revoked_at=EXCLUDED.revoked_at, revoked_by=EXCLUDED.revoked_by`,
		g.ID, g.OrgID, g.ConnectionID, g.AgentID, strs(g.Capabilities), jb(g.ResourceScope), jb(g.Constraints), g.MaxRisk, g.AutonomyOverride, jb(g.Limits),
		g.RedactionProfile, g.IsDefault, g.Alias, g.Status, g.ValidFrom, g.ValidUntil, g.GrantedBy, g.RequestedBy, g.ApprovedBy, g.CreatedAt, g.RevokedAt, g.RevokedBy)
}

func (c *ConnStore) GetGrant(ctx context.Context, org, connID, agentID string) (connections.Grant, error) {
	v, err := one(ctx, c.S, org, scanGrant, `SELECT `+grantCols+` FROM connection_grants
		WHERE org_id=$1 AND connection_id=$2 AND agent_id=$3 AND status IN ('active','pending_second_approval','suspended')`, org, connID, agentID)
	return v, nf(err)
}

func (c *ConnStore) ListGrantsByConnection(ctx context.Context, org, connID string) ([]connections.Grant, error) {
	return many(ctx, c.S, org, scanGrant, `SELECT `+grantCols+` FROM connection_grants WHERE org_id=$1 AND connection_id=$2 ORDER BY created_at`, org, connID)
}

func (c *ConnStore) ListGrantsByAgent(ctx context.Context, org, agentID string) ([]connections.Grant, error) {
	return many(ctx, c.S, org, scanGrant, `SELECT `+grantCols+` FROM connection_grants WHERE org_id=$1 AND agent_id=$2 ORDER BY created_at`, org, agentID)
}

func (c *ConnStore) RevokeGrants(ctx context.Context, org, connID, by string, at time.Time) (int, error) {
	tag, err := c.S.execTag(ctx, org, `UPDATE connection_grants SET status='revoked', revoked_at=$3, revoked_by=$4
		WHERE org_id=$1 AND connection_id=$2 AND status <> 'revoked'`, org, connID, at, by)
	return int(tag.RowsAffected()), err
}

// ---- usage ----

const usageCols = `id, org_id, connection_id, grant_id, agent_id, task_id, approval_id, on_behalf_of, tool, action, capability, decision, deny_reason,
	status, resource_ref, items_count, bytes_in, bytes_out, latency_ms, provider_status, error_code, cost_usd::float8, tainted, created_at`

func scanUsage(r scanner) (connections.Usage, error) {
	var u connections.Usage
	err := r.Scan(&u.ID, &u.OrgID, &u.ConnectionID, &u.GrantID, &u.AgentID, &u.TaskID, &u.ApprovalID, &u.OnBehalfOf, &u.Tool, &u.Action, &u.Capability,
		&u.Decision, &u.DenyReason, &u.Status, &u.ResourceRef, &u.ItemsCount, &u.BytesIn, &u.BytesOut, &u.LatencyMS, &u.ProviderState, &u.ErrorCode,
		&u.CostUSD, &u.Tainted, &u.CreatedAt)
	return u, err
}

func (c *ConnStore) AddUsage(ctx context.Context, u connections.Usage) error {
	return c.S.exec(ctx, u.OrgID, `INSERT INTO connection_usage (`+strings.ReplaceAll(usageCols, "::float8", "")+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		u.ID, u.OrgID, u.ConnectionID, u.GrantID, u.AgentID, u.TaskID, u.ApprovalID, u.OnBehalfOf, u.Tool, u.Action, u.Capability, u.Decision, u.DenyReason,
		u.Status, u.ResourceRef, u.ItemsCount, u.BytesIn, u.BytesOut, u.LatencyMS, u.ProviderState, u.ErrorCode, u.CostUSD, u.Tainted, u.CreatedAt)
}

func (c *ConnStore) ListUsage(ctx context.Context, org string, f connections.UsageFilter) ([]connections.Usage, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	// Cursor = id of the last row seen; ordering is (created_at DESC, id DESC).
	return many(ctx, c.S, org, scanUsage, `SELECT `+usageCols+` FROM connection_usage u WHERE org_id=$1
		AND ($2='' OR connection_id=$2) AND ($3='' OR agent_id=$3) AND ($4='' OR decision=$4)
		AND ($5::timestamptz IS NULL OR created_at >= $5) AND ($6::timestamptz IS NULL OR created_at <= $6)
		AND ($7='' OR (created_at, id) < (SELECT created_at, id FROM connection_usage WHERE org_id=$1 AND id=$7))
		ORDER BY created_at DESC, id DESC LIMIT $8`,
		org, f.ConnectionID, f.AgentID, f.Result, f.From, f.To, f.Before, f.Limit)
}

func (c *ConnStore) CountUsage(ctx context.Context, org, connID, agentID string, since time.Time, writesOnly bool) (int, int64, error) {
	type r struct {
		n int
		b int64
	}
	v, err := one(ctx, c.S, org, func(s scanner) (r, error) {
		var x r
		err := s.Scan(&x.n, &x.b)
		return x, err
	}, `SELECT count(*), COALESCE(sum(bytes_in + bytes_out), 0)::bigint FROM connection_usage
		WHERE org_id=$1 AND connection_id=$2 AND created_at >= $4 AND ($3='' OR agent_id=$3)
		AND decision='allowed' AND status IN ('succeeded','scheduled')
		AND (NOT $5 OR capability NOT LIKE '%.read')`, org, connID, agentID, since, writesOnly)
	return v.n, v.b, err
}

func (c *ConnStore) SumCost(ctx context.Context, org, connID string, since time.Time) (float64, error) {
	return one(ctx, c.S, org, func(s scanner) (float64, error) {
		var f float64
		err := s.Scan(&f)
		return f, err
	}, `SELECT COALESCE(sum(cost_usd), 0)::float8 FROM connection_usage WHERE org_id=$1 AND connection_id=$2 AND created_at >= $3`, org, connID, since)
}

// ---- holds ----

const holdCols = `id, org_id, connection_id, grant_id, agent_id, task_id, approval_id, tool, action, capability, payload, summary, recipients,
	status, reason, hold_until, created_at, decided_by, tainted`

func scanHold(r scanner) (connections.Hold, error) {
	var h connections.Hold
	var p []byte
	err := r.Scan(&h.ID, &h.OrgID, &h.ConnectionID, &h.GrantID, &h.AgentID, &h.TaskID, &h.ApprovalID, &h.Tool, &h.Action, &h.Capability, &p,
		&h.Summary, &h.Recipients, &h.Status, &h.Reason, &h.HoldUntil, &h.CreatedAt, &h.DecidedBy, &h.Tainted)
	unmarshal(p, &h.Payload)
	if h.Recipients == nil {
		h.Recipients = []string{}
	}
	return h, err
}

func (c *ConnStore) PutHold(ctx context.Context, h connections.Hold) error {
	payload := h.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return c.S.exec(ctx, h.OrgID, `INSERT INTO connection_holds (`+holdCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (id) DO UPDATE SET payload=EXCLUDED.payload, status=EXCLUDED.status, reason=EXCLUDED.reason, decided_by=EXCLUDED.decided_by`,
		h.ID, h.OrgID, h.ConnectionID, h.GrantID, h.AgentID, h.TaskID, h.ApprovalID, h.Tool, h.Action, h.Capability, jb(payload), h.Summary, strs(h.Recipients),
		h.Status, h.Reason, h.HoldUntil, h.CreatedAt, h.DecidedBy, h.Tainted)
}

func (c *ConnStore) GetHold(ctx context.Context, org, id string) (connections.Hold, error) {
	v, err := one(ctx, c.S, org, scanHold, `SELECT `+holdCols+` FROM connection_holds WHERE org_id=$1 AND id=$2`, org, id)
	return v, nf(err)
}

func (c *ConnStore) UpdateHold(ctx context.Context, h connections.Hold) error {
	return c.PutHold(ctx, h)
}

func (c *ConnStore) ListHolds(ctx context.Context, org, status string) ([]connections.Hold, error) {
	return many(ctx, c.S, org, scanHold, `SELECT `+holdCols+` FROM connection_holds WHERE org_id=$1 AND ($2='' OR status=$2) ORDER BY created_at`, org, status)
}

func (c *ConnStore) orgIDs(ctx context.Context) []string {
	ids, _ := c.S.ListOrgIDs(ctx)
	seen := map[string]bool{}
	var out []string
	for _, id := range append(ids, c.ExtraOrgs...) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ListDueHolds visits every organization (RLS forbids a cross-tenant query).
func (c *ConnStore) ListDueHolds(ctx context.Context, now time.Time) ([]connections.Hold, error) {
	var out []connections.Hold
	for _, org := range c.orgIDs(ctx) {
		hs, err := many(ctx, c.S, org, scanHold, `SELECT `+holdCols+` FROM connection_holds WHERE org_id=$1 AND status='held' AND hold_until <= $2`, org, now)
		if err != nil {
			return nil, err
		}
		out = append(out, hs...)
	}
	return out, nil
}

// ---- controls ----

func (c *ConnStore) GetOrg(ctx context.Context, org string) (controls.OrgState, error) {
	st, err := one(ctx, c.S, org, func(r scanner) (controls.OrgState, error) {
		var s controls.OrgState
		var set []byte
		err := r.Scan(&s.Mode, &s.KillSwitchLevel, &s.Reason, &s.SetBy, &s.SetAt, &set, &s.DisabledTools)
		unmarshal(set, &s.Settings)
		return s, err
	}, `SELECT mode, kill_switch_level, reason, set_by, set_at, settings, disabled_tools FROM org_controls WHERE org_id=$1`, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return controls.OrgState{Mode: controls.ModeNormal, KillSwitchLevel: controls.LevelNone, DisabledTools: []string{}}, nil
	}
	return st, err
}

func (c *ConnStore) PutOrg(ctx context.Context, org string, s controls.OrgState) error {
	return c.S.exec(ctx, org, `INSERT INTO org_controls (org_id, mode, kill_switch_level, reason, set_by, set_at, settings, disabled_tools)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (org_id) DO UPDATE SET mode=EXCLUDED.mode, kill_switch_level=EXCLUDED.kill_switch_level, reason=EXCLUDED.reason,
			set_by=EXCLUDED.set_by, set_at=EXCLUDED.set_at, settings=EXCLUDED.settings, disabled_tools=EXCLUDED.disabled_tools`,
		org, s.Mode, s.KillSwitchLevel, s.Reason, s.SetBy, s.SetAt, jb(s.Settings), strs(s.DisabledTools))
}

func scanAgentControl(r scanner) (controls.AgentControl, error) {
	var a controls.AgentControl
	err := r.Scan(&a.AgentID, &a.Control, &a.Drain, &a.ReadOnly, &a.PausedBy, &a.PausedAt, &a.PauseReason)
	return a, err
}

func (c *ConnStore) GetAgent(ctx context.Context, org, id string) (controls.AgentControl, error) {
	a, err := one(ctx, c.S, org, scanAgentControl, `SELECT agent_id, control, drain, read_only, paused_by, paused_at, pause_reason FROM agent_controls WHERE org_id=$1 AND agent_id=$2`, org, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return controls.AgentControl{AgentID: id, Control: controls.AgentActive}, nil
	}
	return a, err
}

func (c *ConnStore) PutAgent(ctx context.Context, org string, a controls.AgentControl) error {
	return c.S.exec(ctx, org, `INSERT INTO agent_controls (org_id, agent_id, control, drain, read_only, paused_by, paused_at, pause_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET control=EXCLUDED.control, drain=EXCLUDED.drain, read_only=EXCLUDED.read_only,
			paused_by=EXCLUDED.paused_by, paused_at=EXCLUDED.paused_at, pause_reason=EXCLUDED.pause_reason`,
		org, a.AgentID, a.Control, a.Drain, a.ReadOnly, a.PausedBy, a.PausedAt, a.PauseReason)
}

func (c *ConnStore) ListAgents(ctx context.Context, org string) ([]controls.AgentControl, error) {
	return many(ctx, c.S, org, scanAgentControl, `SELECT agent_id, control, drain, read_only, paused_by, paused_at, pause_reason FROM agent_controls WHERE org_id=$1 ORDER BY agent_id`, org)
}
