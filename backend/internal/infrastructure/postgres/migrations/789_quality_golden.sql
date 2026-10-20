-- Golden cases for the per-agent quality score (docs/architecture/quality-score.md, feature B11).
--
-- The score itself is computed from tables that already exist (approvals, tasks, events); only the golden
-- cases and their recorded runs are new. A case is "input -> expected checks" for one agent; a run is the
-- pass/fail result somebody (or an external harness) recorded. Runs hold no output text and have no foreign key
-- to the case: deleting a case never rewrites the history behind a score. Idempotent. Tenant isolation follows
-- 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS quality_golden_cases (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id         TEXT NOT NULL,
    agent_id   TEXT NOT NULL,
    name       TEXT NOT NULL,
    input      TEXT NOT NULL,
    checks     JSONB NOT NULL DEFAULT '[]',
    active     BOOLEAN NOT NULL DEFAULT true,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS quality_golden_cases_agent_idx ON quality_golden_cases (org_id, agent_id, created_at);

CREATE TABLE IF NOT EXISTS quality_golden_runs (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    case_id       TEXT NOT NULL,
    agent_id      TEXT NOT NULL,
    passed        BOOLEAN NOT NULL,
    checks_passed INT NOT NULL DEFAULT 0,
    checks_total  INT NOT NULL DEFAULT 0,
    task_id       TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    recorded_by   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS quality_golden_runs_case_idx  ON quality_golden_runs (org_id, case_id, created_at DESC);
CREATE INDEX IF NOT EXISTS quality_golden_runs_agent_idx ON quality_golden_runs (org_id, agent_id, created_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['quality_golden_cases', 'quality_golden_runs']
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
