-- Enterprise identity, part 1 (docs/architecture/identity.md): per-organization OIDC single sign-on and sign-in policy.
-- Idempotent. Tenant isolation as 202_rls_policies.sql.
--
-- The OIDC client secret is stored ONLY as ciphertext (sealed_secret: AES-256-GCM under the organization's data key,
-- wrapped by the KEK that lives outside the database, see internal/vault Seal/Unseal). The API never returns it.
CREATE TABLE IF NOT EXISTS org_sso_configs (
    org_id                 TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled                BOOLEAN NOT NULL DEFAULT false,
    issuer                 TEXT NOT NULL,
    client_id              TEXT NOT NULL,
    sealed_secret          BYTEA,
    scopes                 TEXT NOT NULL DEFAULT 'openid email profile',
    default_role           TEXT NOT NULL DEFAULT 'member' CHECK (default_role IN ('admin', 'member', 'viewer')),
    jit                    BOOLEAN NOT NULL DEFAULT false,
    allow_unverified_email BOOLEAN NOT NULL DEFAULT false,
    updated_by             TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- What the organization demands at sign-in (read by internal/auth through the PolicyProvider hook).
CREATE TABLE IF NOT EXISTS org_identity_policy (
    org_id      TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    require_mfa BOOLEAN NOT NULL DEFAULT false,
    enforce_sso BOOLEAN NOT NULL DEFAULT false,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_sso_configs', 'org_identity_policy']
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
