-- AI payroll and return (B15, docs/architecture/ai-payroll-roi.md): the per-organization model behind the
-- ESTIMATED hours saved.
--
-- hourly_rate_usd: the rate the organization itself entered. NULL = not set, and the report then shows hours only
--                  (there is no default rate on purpose).
-- default_minutes: human minutes assumed per completed task when the role has no value of its own.
-- role_minutes:    {"role": minutes} overrides per agent role (0 = that role is not counted).
-- No row means "never configured": the report then uses the built-in assumption (30 minutes) and says so.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS roi_settings (
    org_id          TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    hourly_rate_usd DOUBLE PRECISION CHECK (hourly_rate_usd IS NULL OR (hourly_rate_usd > 0 AND hourly_rate_usd <= 10000)),
    default_minutes INT NOT NULL DEFAULT 30 CHECK (default_minutes BETWEEN 0 AND 480),
    role_minutes    JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    ALTER TABLE roi_settings ENABLE ROW LEVEL SECURITY;
    ALTER TABLE roi_settings FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON roi_settings;
    CREATE POLICY tenant_isolation ON roi_settings USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON roi_settings TO app_user;
    END IF;
END
$$;
