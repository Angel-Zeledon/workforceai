-- F1 "Objetivos de la empresa" (docs/architecture/goals.md): a short list of goals of the organization, optionally of
-- one department (office_id = the department id of 815_offices.sql; '' = the whole company), and the link of the work
-- to them:
--   goals            one row per goal; never deleted by the application (archived instead), so the work that points
--                    to it keeps saying what it contributed to
--   requests.goal_id the goal a request contributes to (NULL = none). The composite foreign key (org_id, goal_id)
--                    makes a goal of another organization impossible at the database level too.
-- Projects carry theirs in their JSONB document (projects.data->>'goal_id') and pass it to the request they launch.
--
-- The cap of active goals and the text limits are checked by the service; the CHECKs below are the backstop.
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS goals (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    office_id   TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 280),
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'achieved', 'archived')),
    period      TEXT NOT NULL DEFAULT '' CHECK (char_length(period) <= 40),
    due_date    DATE,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS goals_status_idx ON goals (org_id, status, created_at DESC);

ALTER TABLE requests ADD COLUMN IF NOT EXISTS goal_id TEXT;
CREATE INDEX IF NOT EXISTS requests_goal_idx ON requests (org_id, goal_id) WHERE goal_id IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'requests_goal_fk') THEN
        ALTER TABLE requests ADD CONSTRAINT requests_goal_fk FOREIGN KEY (org_id, goal_id) REFERENCES goals (org_id, id);
    END IF;
    ALTER TABLE goals ENABLE ROW LEVEL SECURITY;
    ALTER TABLE goals FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON goals;
    CREATE POLICY tenant_isolation ON goals USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON goals TO app_user;
        REVOKE DELETE ON goals FROM app_user;
    END IF;
END
$$;
