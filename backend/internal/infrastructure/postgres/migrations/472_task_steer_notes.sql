-- V2 real-time intervention: notes a human sends to steer a task. Delivered as
-- extra guidance on the runtime calls of the task. Idempotent; RLS by org.
CREATE TABLE IF NOT EXISTS task_steer_notes (
    id           TEXT NOT NULL,
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id      TEXT NOT NULL,
    agent_id     TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL,
    author       TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS task_steer_notes_task_idx ON task_steer_notes (org_id, task_id, created_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['task_steer_notes']
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
