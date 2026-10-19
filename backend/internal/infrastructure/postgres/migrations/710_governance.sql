-- Governance (docs/architecture/governance.md): department tag on agents, department spend limits and the
-- per-organization data retention policy.
--
-- agents.department is a plain nullable string tag (another stream builds real departments; this only groups).
-- org_retention: days to keep chats / files / audit (0 = keep for ever). next_run_at lets replicas claim a purge
-- run atomically. The audit trail is append-only and hash-chained: audit_days is stored and validated but the
-- purge job never deletes audit rows (see governance.md).
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

ALTER TABLE agents ADD COLUMN IF NOT EXISTS department TEXT;
CREATE INDEX IF NOT EXISTS agents_department_idx ON agents (org_id, department) WHERE department IS NOT NULL;

CREATE TABLE IF NOT EXISTS department_limits (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    department  TEXT NOT NULL CHECK (char_length(department) BETWEEN 1 AND 60),
    monthly_usd DOUBLE PRECISION NOT NULL CHECK (monthly_usd > 0),
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, department)
);

CREATE TABLE IF NOT EXISTS org_retention (
    org_id      TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    chats_days  INT NOT NULL DEFAULT 0 CHECK (chats_days >= 0),
    files_days  INT NOT NULL DEFAULT 0 CHECK (files_days >= 0),
    audit_days  INT NOT NULL DEFAULT 0 CHECK (audit_days >= 0),
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_run_at TIMESTAMPTZ,
    last_result JSONB NOT NULL DEFAULT '{}',
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['department_limits', 'org_retention']
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
