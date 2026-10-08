-- Durable execution (A1): what a request execution needs to continue after a
-- restart. Spec: application/durable.go, docs/architecture/07-seguridad-costos.md 5.4.
--
-- request_runs: one row per request, written when processing starts and after
-- the plan review (who asked, conversation, "no external actions", removed tasks).
-- task_checkpoints: one row per task waiting on a tool approval (the tool
-- requests still to handle, the task output and its taint); deleted when the
-- task ends. Tool arguments are stored as the runtime returned them (sanitized,
-- never credentials: secrets do not reach the runtime).
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS request_runs (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    request_id       TEXT NOT NULL,
    requested_by     TEXT NOT NULL DEFAULT '',
    conversation_id  TEXT NOT NULL DEFAULT '',
    read_only        BOOLEAN NOT NULL DEFAULT false,
    removed_task_ids JSONB NOT NULL DEFAULT '[]',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, request_id)
);

CREATE TABLE IF NOT EXISTS task_checkpoints (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id     TEXT NOT NULL,
    request_id  TEXT NOT NULL,
    approval_id TEXT NOT NULL,
    tool_index  INT  NOT NULL CHECK (tool_index >= 0),
    tools       JSONB NOT NULL DEFAULT '[]',
    output      JSONB NOT NULL DEFAULT '{}',
    taint       JSONB,
    executed    BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, task_id)
);
CREATE INDEX IF NOT EXISTS task_checkpoints_request_idx ON task_checkpoints (org_id, request_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['request_runs', 'task_checkpoints']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON request_runs TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON task_checkpoints TO app_user;
    END IF;
END
$$;
