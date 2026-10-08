-- Comments and edit proposals on artifacts (docs/architecture/agent-workspaces.md
-- sec. 5.5 and 8). A proposal carries the full suggested content and the version
-- it was based on; accepting it writes a normal artifact version. Only people
-- accept or reject (enforced in the service, and recorded in resolved_by).

CREATE TABLE IF NOT EXISTS artifact_comments (
    id          TEXT NOT NULL,
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    artifact_id TEXT NOT NULL,
    parent_id   TEXT,
    anchor      JSONB,
    author      JSONB NOT NULL,
    body        TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 16000),
    mentions    JSONB NOT NULL DEFAULT '[]',
    resolved    BOOLEAN NOT NULL DEFAULT false,
    resolved_by TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS artifact_comments_artifact_idx ON artifact_comments (org_id, artifact_id, created_at);

CREATE TABLE IF NOT EXISTS artifact_proposals (
    id               TEXT NOT NULL,
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    artifact_id      TEXT NOT NULL,
    base_version     INT  NOT NULL,
    author           JSONB NOT NULL,
    summary          TEXT NOT NULL DEFAULT '',
    content          JSONB NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected')),
    resolved_by      TEXT,
    resolved_note    TEXT NOT NULL DEFAULT '',
    resolved_version INT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at      TIMESTAMPTZ,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS artifact_proposals_artifact_idx ON artifact_proposals (org_id, artifact_id, created_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['artifact_comments', 'artifact_proposals']
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
        GRANT SELECT, INSERT, UPDATE ON artifact_comments, artifact_proposals TO app_user;
        REVOKE DELETE ON artifact_comments, artifact_proposals FROM app_user;
    END IF;
END
$$;
