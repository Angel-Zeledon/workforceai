-- Plans with a HARD spend cap (docs/architecture/approvals-notify.md).
--
-- org_plans: one row per organization that has an explicit plan. No row means
-- the deployment default (FREE_PLAN_CAP_USD > 0 makes it the free plan, else no
-- plan cap). cap_usd is the hard cap of a free plan, in USD, over the lifetime
-- of the organization. cap_notified_at records the last "your work is paused,
-- the cap was reached" notice so it is sent once, not on every blocked call.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_plans (
    org_id          TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    plan            TEXT NOT NULL DEFAULT 'free' CHECK (plan IN ('free','paid')),
    cap_usd         DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (cap_usd >= 0),
    cap_notified_at TIMESTAMPTZ,
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    EXECUTE 'ALTER TABLE org_plans ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE org_plans FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON org_plans';
    EXECUTE 'CREATE POLICY tenant_isolation ON org_plans
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())';
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON org_plans TO app_user;
    END IF;
END
$$;
