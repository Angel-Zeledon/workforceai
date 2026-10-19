-- Enterprise identity, part 4: SCIM 2.0 provisioning. Idempotent.
--
-- scim_tokens: ONE bearer token per organization, stored as its SHA-256 only (the token is shown once). The lookup is by
-- hash before any tenant is known, so the table is not under RLS (same reasoning as refresh_tokens); a hash alone grants
-- nothing. scim_users: the accounts SCIM created or adopted, with the role SCIM maps them to; tenant data (RLS). SCIM
-- only ever touches users listed here, so it can never alter an account it did not provision.
CREATE TABLE IF NOT EXISTS scim_tokens (
    token_hash   TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE CASCADE,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS scim_users (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL DEFAULT '',
    user_name   TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT true,
    role        TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member', 'viewer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS scim_users_name_idx ON scim_users (org_id, lower(user_name));

ALTER TABLE scim_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_users FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON scim_users;
CREATE POLICY tenant_isolation ON scim_users
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON scim_users TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON scim_tokens TO app_user;
    END IF;
END
$$;
