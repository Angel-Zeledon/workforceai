-- Durable schedule holds (docs/architecture/organization.md, "Holds"): a task held by the working schedule of its
-- agent sleeps until the next opening (wake_at). The row lets a restart (or another instance) bring the
-- request back and resume it once the opening has passed (internal/application/hold.go).
-- One row per task. No foreign key to tasks: a stale row is dropped by the watcher when its task is no longer
-- pending. Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS schedule_holds (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id    TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    agent_id   TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    wake_at    TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, task_id)
);

CREATE INDEX IF NOT EXISTS schedule_holds_wake_idx ON schedule_holds (org_id, wake_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['schedule_holds']
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
