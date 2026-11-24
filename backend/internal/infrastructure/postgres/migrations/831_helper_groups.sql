-- Temporary helpers (docs/superpowers/specs/2026-11-23-ayudantes-temporales-design.md): one request split in
-- independent parts, each an ordinary task of the responsible employee. A helper is not an agent: no row in agents.
--
-- tasks.helper marks the task as a part ({group_id, index, total, label}; NULL = ordinary task).
-- helper_groups holds the accepted group: who accepted it, how many parts, the hard USD cap and the real spend.
-- parts is a JSON array [{index, label, task_id, status, text, files}] (the material of every part).
-- One group per request (the second click on "Launch" finds the first).
--
-- Additive and idempotent. Tenant isolation follows 815_offices.sql / 830. Down:
-- backend/migrations/down/831_helper_groups_down.sql.

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS helper JSONB;

CREATE TABLE IF NOT EXISTS helper_groups (
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id             TEXT NOT NULL,
    request_id     TEXT NOT NULL,
    owner_agent_id TEXT NOT NULL,
    mode           TEXT NOT NULL CHECK (mode IN ('position', 'free')),
    count          INT  NOT NULL CHECK (count >= 0),
    max_count      INT  NOT NULL DEFAULT 0,
    parallel       INT  NOT NULL DEFAULT 1 CHECK (parallel >= 1),
    parts          JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(parts) = 'array'),
    estimate_min   DOUBLE PRECISION NOT NULL DEFAULT 0,
    estimate_max   DOUBLE PRECISION NOT NULL DEFAULT 0,
    cap_usd        DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (cap_usd >= 0),
    spent_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    status         TEXT NOT NULL CHECK (status IN ('proposed', 'running', 'joining', 'done', 'stopped', 'failed', 'declined')),
    accepted_by    TEXT NOT NULL DEFAULT '',
    union_task_id  TEXT NOT NULL DEFAULT '',
    stop_reason    TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS helper_groups_request_idx ON helper_groups (org_id, request_id);
CREATE INDEX IF NOT EXISTS helper_groups_status_idx ON helper_groups (org_id, status, created_at);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['helper_groups'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
