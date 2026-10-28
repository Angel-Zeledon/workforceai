-- Rehearsal (B9, "Ensayo general"): agents show what they WOULD have sent or paid without doing it.
--   agent_rehearsal     the per-agent switch (configuration; survives a demo reset)
--   rehearsal_actions   one row per side-effect tool call that a rehearsal recorded and did NOT execute, with the
--                       human verdict (would_approve / would_reject). Metadata and a short sanitized preview of the
--                       arguments only: never a credential, never the raw arguments.
-- Spec: docs/architecture/rehearsal.md.
--
-- Forward-only and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS agent_rehearsal (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT false,
    set_by     TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);

CREATE TABLE IF NOT EXISTS rehearsal_actions (
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id             TEXT NOT NULL,
    request_id     TEXT NOT NULL DEFAULT '',
    task_id        TEXT NOT NULL DEFAULT '',
    agent_id       TEXT NOT NULL,
    tool           TEXT NOT NULL,
    action         TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT '',
    scope          TEXT NOT NULL DEFAULT '',
    would_decision TEXT NOT NULL CHECK (would_decision IN ('allowed', 'needs_approval', 'denied')),
    deny_reason    TEXT NOT NULL DEFAULT '',
    risk           TEXT NOT NULL DEFAULT '',
    reversibility  TEXT NOT NULL DEFAULT '',
    account        TEXT NOT NULL DEFAULT '',
    recipients     JSONB NOT NULL DEFAULT '[]',
    amount         DOUBLE PRECISION,
    flags          JSONB NOT NULL DEFAULT '[]',
    preview        JSONB NOT NULL DEFAULT '{}',
    args_hash      TEXT NOT NULL DEFAULT '',
    verdict        TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('', 'would_approve', 'would_reject')),
    verdict_by     TEXT NOT NULL DEFAULT '',
    verdict_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);

CREATE INDEX IF NOT EXISTS rehearsal_actions_request_idx ON rehearsal_actions (org_id, request_id, created_at DESC);
CREATE INDEX IF NOT EXISTS rehearsal_actions_agent_idx ON rehearsal_actions (org_id, agent_id, created_at DESC);

DO $$
BEGIN
    ALTER TABLE agent_rehearsal ENABLE ROW LEVEL SECURITY;
    ALTER TABLE agent_rehearsal FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON agent_rehearsal;
    CREATE POLICY tenant_isolation ON agent_rehearsal USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());

    ALTER TABLE rehearsal_actions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE rehearsal_actions FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON rehearsal_actions;
    CREATE POLICY tenant_isolation ON rehearsal_actions USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON agent_rehearsal TO app_user;
        -- Rows are written once; only the human verdict is updated later (Reset deletes them).
        GRANT SELECT, INSERT, UPDATE, DELETE ON rehearsal_actions TO app_user;
    END IF;
END
$$;
