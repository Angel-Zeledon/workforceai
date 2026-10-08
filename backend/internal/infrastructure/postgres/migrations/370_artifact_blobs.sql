-- Uploaded PDF files of pdf artifacts (docs/architecture/agent-workspaces.md
-- sec. 5.7, simplified for A7): the bytes live in Postgres (bytea) so the
-- docker-compose stack needs no extra volume or object store. Blobs are
-- immutable: a new upload is a new row and a new artifact version pointing at
-- it; old versions keep pointing at the old blob. Size is capped by the API
-- (25 MB) and by the CHECK below.

CREATE TABLE IF NOT EXISTS artifact_blobs (
    id          TEXT NOT NULL,
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    artifact_id TEXT NOT NULL,
    mime        TEXT NOT NULL CHECK (mime = 'application/pdf'),
    filename    TEXT NOT NULL DEFAULT '',
    sha256      TEXT NOT NULL,
    size_bytes  INT  NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
    data        BYTEA NOT NULL,
    created_by  JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE,
    CHECK (octet_length(data) = size_bytes)
);
CREATE INDEX IF NOT EXISTS artifact_blobs_artifact_idx ON artifact_blobs (org_id, artifact_id, created_at);

ALTER TABLE artifact_blobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE artifact_blobs FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON artifact_blobs;
CREATE POLICY tenant_isolation ON artifact_blobs
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        -- Immutable for the application: insert and read only.
        GRANT SELECT, INSERT ON artifact_blobs TO app_user;
        REVOKE UPDATE, DELETE ON artifact_blobs FROM app_user;
    END IF;
END
$$;
