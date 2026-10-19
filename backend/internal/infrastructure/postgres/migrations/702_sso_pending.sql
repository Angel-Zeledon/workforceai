-- Enterprise identity, part 3: single-use SSO login records. Idempotent.
--
-- kind 'state'    : created by /sso/start, redeemed once by /sso/callback (carries the nonce and PKCE verifier).
-- kind 'exchange' : created by /sso/callback, redeemed once by POST /auth/sso/exchange (carries the proven identity).
-- Only the SHA-256 of the value the browser holds is stored. Rows live minutes and are deleted when redeemed.
-- Like users/refresh_tokens (201_auth_tables.sql) this table is read before a tenant is known, so it is not under RLS;
-- it is touched only by internal/sso.
CREATE TABLE IF NOT EXISTS sso_pending (
    kind       TEXT NOT NULL CHECK (kind IN ('state', 'exchange')),
    hash       TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    nonce      TEXT NOT NULL DEFAULT '',
    verifier   TEXT NOT NULL DEFAULT '',
    identity   JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (kind, hash)
);
CREATE INDEX IF NOT EXISTS sso_pending_expiry_idx ON sso_pending (expires_at);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON sso_pending TO app_user;
    END IF;
END
$$;
