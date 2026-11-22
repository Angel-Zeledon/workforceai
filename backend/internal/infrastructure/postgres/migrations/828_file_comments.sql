-- F7 "Comentarios anclados" (docs/architecture/anchored-comments.md): notes on a passage of a Drive file.
--   anchor   JSONB {quote, prefix, suffix, version, start, end}: where the comment was made. It is located again
--            against each version; if the quote is gone the comment stays and reads "the text changed".
--   change   JSONB {agent_id, base_version, requested_by, requested_at, turn_id}: the request "change this passage"
--            made from the comment. When that agent delivers a newer version the comment is resolved
--            (resolved_by = 'agent:<id>', resolved_version = that version).
-- Comments are never deleted by the application (resolved instead). The composite foreign key keeps a comment of one
-- organization from pointing at a file of another. Additive and idempotent; tenant isolation follows
-- 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS file_comments (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id               TEXT NOT NULL,
    file_id          TEXT NOT NULL,
    parent_id        TEXT,
    anchor           JSONB,
    author           JSONB NOT NULL,
    body             TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
    resolved         BOOLEAN NOT NULL DEFAULT false,
    resolved_by      TEXT,
    resolved_version INT,
    resolved_at      TIMESTAMPTZ,
    change           JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, file_id) REFERENCES drive_files (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS file_comments_file_idx ON file_comments (org_id, file_id, created_at);

DO $$
BEGIN
    ALTER TABLE file_comments ENABLE ROW LEVEL SECURITY;
    ALTER TABLE file_comments FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON file_comments;
    CREATE POLICY tenant_isolation ON file_comments USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON file_comments TO app_user;
        REVOKE DELETE ON file_comments FROM app_user;
    END IF;
END
$$;
