-- Connections: credentials vault, third-party connections, per-agent grants,
-- usage log, held (deferred) sends and pending OAuth authorizations.
-- Spec: docs/architecture/integrations-credentials.md sections 3 and 6.
-- (The spec's provisional numbers 205/206 became 240/241 after the real
-- migrations 210, 230 of cost control and organization config.)
--
-- Secrets: ONLY credentials.ciphertext holds secret material, encrypted with
-- AES-256-GCM under a per-organization data key wrapped by a KEK that lives
-- outside the database. Only the vault code reads or writes that table.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql; oauth_states has
-- one extra policy branch so the (unauthenticated) OAuth callback can consume
-- its own state by presenting the unguessable state hash.

-- Data keys per organization (envelope encryption).
CREATE TABLE IF NOT EXISTS org_data_keys (
    org_id      TEXT NOT NULL REFERENCES organizations(id),
    version     INT  NOT NULL,
    kek_id      TEXT NOT NULL,
    wrapped_dek BYTEA NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('active', 'retired')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at  TIMESTAMPTZ,
    PRIMARY KEY (org_id, version)
);

CREATE TABLE IF NOT EXISTS connections (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    provider      TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('oauth2', 'app_installation', 'api_key', 'bearer', 'basic', 'imap_smtp')),
    label         TEXT NOT NULL,
    account_label TEXT NOT NULL DEFAULT '',
    account_ref   TEXT NOT NULL DEFAULT '',   -- hash of the provider's stable account id
    mode          TEXT NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'simulated')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN
                    ('pending', 'active', 'needs_reauth', 'error', 'suspended', 'revoked', 'expired')),
    status_reason TEXT NOT NULL DEFAULT '',
    class         TEXT NOT NULL DEFAULT 'read' CHECK (class IN ('read', 'write', '')),
    requested_capabilities TEXT[] NOT NULL DEFAULT '{}',
    granted_capabilities   TEXT[] NOT NULL DEFAULT '{}',
    provider_scopes        TEXT[] NOT NULL DEFAULT '{}',
    resource_scope JSONB NOT NULL DEFAULT '{}',
    limits        JSONB NOT NULL DEFAULT '{}',
    read_only     BOOLEAN NOT NULL DEFAULT false,
    last_tested_at  TIMESTAMPTZ,
    last_test_status TEXT NOT NULL DEFAULT '',
    last_used_at    TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    expires_at    TIMESTAMPTZ,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    revoked_by    TEXT NOT NULL DEFAULT '',
    pending_provider_revocation BOOLEAN NOT NULL DEFAULT false,
    -- bring-your-own OAuth app (self-hosted): the client id is public, the secret is sealed by the vault
    oauth_client_id TEXT NOT NULL DEFAULT '',
    oauth_client_secret_enc BYTEA,
    UNIQUE (org_id, id)
);
CREATE INDEX IF NOT EXISTS connections_org_idx ON connections (org_id, created_at);

-- One row per secret version. Never selected outside the vault.
CREATE TABLE IF NOT EXISTS credentials (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    connection_id TEXT NOT NULL,
    version       INT  NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('active', 'next', 'retired', 'destroyed')),
    kind          TEXT NOT NULL,
    ciphertext    BYTEA,            -- NULL once destroyed (crypto-shred)
    nonce         BYTEA,            -- 96-bit random nonce, never reused with the same DEK
    dek_version   INT NOT NULL,
    hint          TEXT NOT NULL DEFAULT '',   -- last 4 chars, only for secrets >= 20 chars
    expires_at    TIMESTAMPTZ,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at    TIMESTAMPTZ,
    destroyed_at  TIMESTAMPTZ,
    UNIQUE (org_id, connection_id, version),
    FOREIGN KEY (org_id, connection_id) REFERENCES connections (org_id, id) ON DELETE CASCADE
);
-- AAD of the encryption = org_id|credential id|version|kind (recomputed, not stored).

CREATE TABLE IF NOT EXISTS connection_grants (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    connection_id TEXT NOT NULL,
    agent_id      TEXT NOT NULL,
    capabilities  TEXT[] NOT NULL,
    resource_scope JSONB NOT NULL DEFAULT '{}',
    constraints   JSONB NOT NULL DEFAULT '{}',
    max_risk      TEXT NOT NULL DEFAULT 'low' CHECK (max_risk IN ('low', 'medium', 'high')),
    autonomy_override TEXT NOT NULL DEFAULT '' CHECK (autonomy_override IN ('', 'suggest', 'approve_each', 'rules', 'autonomous')),
    limits        JSONB NOT NULL DEFAULT '{}',
    -- owner decision 6: the default redaction profile is strict
    redaction_profile TEXT NOT NULL DEFAULT 'strict' CHECK (redaction_profile IN ('standard', 'strict', 'none_admin_only')),
    is_default    BOOLEAN NOT NULL DEFAULT true,
    alias         TEXT NOT NULL DEFAULT '',
    -- pending_second_approval: a write grant waiting for a second human (only when the org has > 1 admin)
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending_second_approval', 'suspended', 'revoked', 'expired')),
    valid_from    TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until   TIMESTAMPTZ,
    granted_by    TEXT NOT NULL DEFAULT '',
    requested_by  TEXT NOT NULL DEFAULT '',   -- maker of a write grant (== granted_by); approved_by is the checker
    approved_by   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    revoked_by    TEXT NOT NULL DEFAULT '',
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, connection_id) REFERENCES connections (org_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS connection_grants_one_live
    ON connection_grants (org_id, connection_id, agent_id)
    WHERE status IN ('active', 'pending_second_approval', 'suspended');
CREATE INDEX IF NOT EXISTS connection_grants_agent_idx ON connection_grants (org_id, agent_id);

-- Append-only usage projection. Metadata only, never content.
CREATE TABLE IF NOT EXISTS connection_usage (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    connection_id TEXT NOT NULL,
    grant_id      TEXT NOT NULL DEFAULT '',
    agent_id      TEXT NOT NULL DEFAULT '',
    task_id       TEXT NOT NULL DEFAULT '',
    approval_id   TEXT NOT NULL DEFAULT '',
    on_behalf_of  TEXT NOT NULL DEFAULT '',
    tool          TEXT NOT NULL,
    action        TEXT NOT NULL,
    capability    TEXT NOT NULL,
    decision      TEXT NOT NULL CHECK (decision IN ('allowed', 'needs_approval', 'denied')),
    deny_reason   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'skipped', 'held', 'scheduled', 'sent')),
    resource_ref  TEXT NOT NULL DEFAULT '',
    items_count   INT NOT NULL DEFAULT 0,
    bytes_in      BIGINT NOT NULL DEFAULT 0,
    bytes_out     BIGINT NOT NULL DEFAULT 0,
    latency_ms    INT NOT NULL DEFAULT 0,
    provider_status INT NOT NULL DEFAULT 0,
    error_code    TEXT NOT NULL DEFAULT '',
    cost_usd      NUMERIC(12, 6) NOT NULL DEFAULT 0,
    tainted       BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS connection_usage_conn_idx  ON connection_usage (org_id, connection_id, created_at DESC);
CREATE INDEX IF NOT EXISTS connection_usage_agent_idx ON connection_usage (org_id, agent_id, created_at DESC);

-- Pending OAuth authorizations: single use, 10 minute TTL.
CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash    TEXT PRIMARY KEY,           -- sha256(state); the plain state only travels to the provider
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    connection_id TEXT NOT NULL,
    user_id       TEXT NOT NULL DEFAULT '',
    pkce_verifier_enc BYTEA NOT NULL,         -- sealed by the vault
    redirect_uri  TEXT NOT NULL,
    requested_capabilities TEXT[] NOT NULL DEFAULT '{}',
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ
);

-- Deferred external-effect actions (email send): mandatory cancellation window.
CREATE TABLE IF NOT EXISTS connection_holds (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    connection_id TEXT NOT NULL,
    grant_id      TEXT NOT NULL DEFAULT '',
    agent_id      TEXT NOT NULL,
    task_id       TEXT NOT NULL DEFAULT '',
    approval_id   TEXT NOT NULL DEFAULT '',
    tool          TEXT NOT NULL,
    action        TEXT NOT NULL,
    capability    TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}',   -- message to send; emptied once decided
    summary       TEXT NOT NULL DEFAULT '',
    recipients    TEXT[] NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL CHECK (status IN ('held', 'sent', 'cancelled', 'failed')),
    reason        TEXT NOT NULL DEFAULT '',
    hold_until    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by    TEXT NOT NULL DEFAULT '',
    tainted       BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS connection_holds_due_idx ON connection_holds (hold_until) WHERE status = 'held';

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_data_keys', 'connections', 'credentials', 'connection_grants',
                             'connection_usage', 'connection_holds']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;

    -- oauth_states: tenant isolation, plus access by the state hash itself
    -- (app.oauth_state) for the public callback, which has no organization yet.
    ALTER TABLE oauth_states ENABLE ROW LEVEL SECURITY;
    ALTER TABLE oauth_states FORCE  ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON oauth_states;
    CREATE POLICY tenant_isolation ON oauth_states
        USING      (org_id = app_current_org() OR state_hash = NULLIF(current_setting('app.oauth_state', true), ''))
        WITH CHECK (org_id = app_current_org() OR state_hash = NULLIF(current_setting('app.oauth_state', true), ''));
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_states TO app_user;
        -- usage is append-only for the application
        REVOKE UPDATE, DELETE ON connection_usage FROM app_user;
    END IF;
END
$$;
