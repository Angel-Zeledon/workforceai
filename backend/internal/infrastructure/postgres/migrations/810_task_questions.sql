-- "Necesita tu ayuda" (docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md, part 2;
-- docs/architecture/approvals-inbox.md, questions). An agent that cannot get or decide something by itself asks a
-- person: the question is stored here, its task waits in status needs_input (tasks.status is free text) and a person
-- answers with text, an option, a file, by reassigning or by skipping the task. Rows are never deleted by the
-- application: the answer, who gave it and when stay as the history of the task.
--
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS task_questions (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id       TEXT NOT NULL,
    request_id    TEXT NOT NULL,
    agent_id      TEXT NOT NULL DEFAULT '',
    project_id    TEXT NOT NULL DEFAULT '',
    group_key     TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT 'other' CHECK (kind IN ('data', 'file', 'decision', 'contradiction', 'other')),
    what_happened TEXT NOT NULL DEFAULT '' CHECK (char_length(what_happened) <= 600),
    need          TEXT NOT NULL DEFAULT '' CHECK (char_length(need) <= 600),
    options       TEXT[] NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'answered', 'reassigned', 'skipped', 'cancelled')),
    asked_for     TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ,
    resolved_by   TEXT NOT NULL DEFAULT '',
    answer_kind   TEXT NOT NULL DEFAULT '',
    answer        TEXT NOT NULL DEFAULT '' CHECK (char_length(answer) <= 2000),
    file_path     TEXT NOT NULL DEFAULT '',
    reassign_to   TEXT NOT NULL DEFAULT '',
    reminders     INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS task_questions_status_idx ON task_questions (org_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS task_questions_task_idx ON task_questions (org_id, task_id, created_at);

DO $$
BEGIN
    ALTER TABLE task_questions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE task_questions FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON task_questions;
    CREATE POLICY tenant_isolation ON task_questions USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON task_questions TO app_user;
        -- The answers are the history of the task.
        REVOKE DELETE ON task_questions FROM app_user;
    END IF;
END
$$;
