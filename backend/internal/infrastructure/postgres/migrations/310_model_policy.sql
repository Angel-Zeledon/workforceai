-- Organization model policy (A3, docs/architecture/07-seguridad-costos.md):
-- which model providers may receive the organization's data, which are tried
-- first, and the provider order / model per agent role. One row per
-- organization; no row = no organization restriction (the server ceiling
-- ALLOWED_PROVIDERS still applies). Contains no secrets.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_model_policy (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    policy     JSONB NOT NULL DEFAULT '{}',
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE org_model_policy ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_model_policy FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON org_model_policy;
CREATE POLICY tenant_isolation ON org_model_policy
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON org_model_policy TO app_user;
        REVOKE DELETE ON org_model_policy FROM app_user;
    END IF;
END
$$;
