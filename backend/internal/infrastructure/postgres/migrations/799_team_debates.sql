-- Team debates (B5): before a high-impact decision 2 to 4 agents exchange a bounded number of short turns and the
-- assistant writes a synthesis (recommended option + dissent). The transcript is model output (untrusted) and
-- advisory: it never approves or executes anything. Failed attempts are kept too (status 'failed', no turns) so
-- the meetings view can say why a debate did not happen. Spec: docs/architecture/team-debates.md.
--
-- Forward-only and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS team_debates (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    topic        TEXT NOT NULL,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('planner_decision', 'user_request')),
    request_id   TEXT NOT NULL DEFAULT '',
    task_id      TEXT NOT NULL DEFAULT '',
    options      JSONB NOT NULL DEFAULT '[]',
    participants JSONB NOT NULL DEFAULT '[]',
    turns        JSONB NOT NULL DEFAULT '[]',
    synthesis    JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL CHECK (status IN ('done', 'failed')),
    fail_code    TEXT NOT NULL DEFAULT '',
    max_turns    INTEGER NOT NULL DEFAULT 0 CHECK (max_turns >= 0),
    locale       TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL DEFAULT '',
    cost_usd     DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);

CREATE INDEX IF NOT EXISTS team_debates_recent ON team_debates (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS team_debates_request ON team_debates (org_id, request_id) WHERE request_id <> '';

DO $$
BEGIN
    ALTER TABLE team_debates ENABLE ROW LEVEL SECURITY;
    ALTER TABLE team_debates FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON team_debates;
    CREATE POLICY tenant_isolation ON team_debates USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        -- A debate is written once and never edited by the application (Reset deletes it with the request data).
        GRANT SELECT, INSERT, DELETE ON team_debates TO app_user;
    END IF;
END
$$;
