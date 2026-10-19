-- Corporate organization model, part 2: agent job descriptions (docs/architecture/organization.md).
-- One row per agent: the department it belongs to, who it reports to, who it escalates to, its responsibilities,
-- the tools it may use (always a subset of its role template), its working schedule (hours / days / timezone as
-- JSON; outside the hours the task gate queues the work instead of running it) and the monthly budget
-- (mirrored into budget_caps scope 'agent' so the existing budget guard enforces it). Idempotent.

CREATE TABLE IF NOT EXISTS agent_jobs (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id         TEXT NOT NULL,
    department_id    TEXT NOT NULL DEFAULT '',
    reports_to_kind  TEXT NOT NULL DEFAULT '' CHECK (reports_to_kind IN ('', 'person', 'agent')),
    reports_to_id    TEXT NOT NULL DEFAULT '',
    escalation_kind  TEXT NOT NULL DEFAULT '' CHECK (escalation_kind IN ('', 'person', 'agent')),
    escalation_id    TEXT NOT NULL DEFAULT '',
    responsibilities JSONB NOT NULL DEFAULT '[]',
    allowed_tools    JSONB NOT NULL DEFAULT '[]',
    schedule         JSONB NOT NULL DEFAULT '{}',
    budget_usd       DOUBLE PRECISION CHECK (budget_usd IS NULL OR budget_usd >= 0),
    updated_by       TEXT NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);
CREATE INDEX IF NOT EXISTS agent_jobs_dept_idx ON agent_jobs (org_id, department_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_jobs']
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
