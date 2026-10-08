-- Persistent windowed counters (A1 step 6, A7): the latest JSON snapshot of
-- the per-organization sliding windows of the policy engine (windowed limits,
-- scope 'policy.limits') and of the anomaly detection (scope 'anomaly'), so a
-- restart does not reset them. Spec: internal/counters,
-- docs/architecture/07-seguridad-costos.md 5.5.
--
-- The snapshots hold timestamps, amounts, agent ids, limit ids and recipient
-- domains; never message content or credentials.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS window_counters (
    org_id     TEXT  NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope      TEXT  NOT NULL CHECK (scope <> ''),
    state      JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, scope)
);

ALTER TABLE window_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE window_counters FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON window_counters;
CREATE POLICY tenant_isolation ON window_counters
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON window_counters TO app_user;
    END IF;
END
$$;
