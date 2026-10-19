-- Public API tokens and outbound webhooks (docs/architecture/governance.md).
--
-- api_tokens: only sha256(token) is stored (the token is high entropy and shown once). A bearer token arrives
-- without an organization, so a second, read-only policy lets a transaction that set app.token_hash see exactly
-- the row with that hash (and nothing else); every other access is the normal tenant isolation.
-- webhook_endpoints.secret_enc: the signing secret sealed under the organization's data key (never plaintext).
-- webhook_deliveries: the outbox (one row per event and endpoint) with retry state.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS api_tokens (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    name         TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL,
    scopes       JSONB NOT NULL DEFAULT '[]',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_hash_idx ON api_tokens (token_hash);

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    url             TEXT NOT NULL CHECK (char_length(url) <= 2048),
    description     TEXT NOT NULL DEFAULT '',
    events          JSONB NOT NULL DEFAULT '[]',
    secret_enc      BYTEA NOT NULL,
    secret_hint     TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    disabled_reason TEXT NOT NULL DEFAULT '',
    failure_streak  INT NOT NULL DEFAULT 0,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    endpoint_id     TEXT NOT NULL,
    event           TEXT NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'dead')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status     INT NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, endpoint_id) REFERENCES webhook_endpoints (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS webhook_deliveries_due_idx ON webhook_deliveries (org_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS webhook_deliveries_endpoint_idx ON webhook_deliveries (org_id, endpoint_id, created_at DESC);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['api_tokens', 'webhook_endpoints', 'webhook_deliveries']
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
END
$$;

-- Bearer lookup: read-only, and only the row whose hash the transaction announced.
DROP POLICY IF EXISTS token_lookup ON api_tokens;
CREATE POLICY token_lookup ON api_tokens FOR SELECT
    USING (token_hash = NULLIF(current_setting('app.token_hash', true), ''));
