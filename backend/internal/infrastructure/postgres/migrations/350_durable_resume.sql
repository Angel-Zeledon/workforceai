-- Durable execution, second delivery (A1b): resume what 290_durable_runs.sql
-- could not. Spec: application/durable.go, docs/architecture/07-seguridad-costos.md 5.4.
--
-- request_runs.gate: the human gate a request was waiting on before it started
--   (plan review or cost confirmation), when it began (the original deadline is
--   kept after a restart) and the estimate shown to the human.
-- request_runs.chat: the chat turn that started the request, so the report (or
--   the failure) is echoed into that chat after a restart.
-- task_checkpoints.gateway: the approval card of a Tool Gateway action (args
--   hash, outbox item, recipients); the action itself is the tool request at
--   tool_index. Never credentials: secrets do not reach the runtime.
-- approval_executions: one row per approval whose action was executed. The
--   primary key makes an approved action run at most once, across restarts,
--   paths (task or outbox) and instances; args_hash binds it to the exact call.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

ALTER TABLE request_runs     ADD COLUMN IF NOT EXISTS gate    JSONB;
ALTER TABLE request_runs     ADD COLUMN IF NOT EXISTS chat    JSONB;
ALTER TABLE task_checkpoints ADD COLUMN IF NOT EXISTS gateway JSONB;

CREATE TABLE IF NOT EXISTS approval_executions (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    approval_id TEXT NOT NULL,
    task_id     TEXT NOT NULL DEFAULT '',
    tool        TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT '',
    args_hash   TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'claimed',
    hold_id     TEXT NOT NULL DEFAULT '',
    claimed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, approval_id)
);
CREATE INDEX IF NOT EXISTS approval_executions_task_idx ON approval_executions (org_id, task_id);

DO $$
BEGIN
    ALTER TABLE approval_executions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE approval_executions FORCE  ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON approval_executions;
    CREATE POLICY tenant_isolation ON approval_executions
        USING      (org_id = app_current_org())
        WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON approval_executions TO app_user;
    END IF;
END
$$;
