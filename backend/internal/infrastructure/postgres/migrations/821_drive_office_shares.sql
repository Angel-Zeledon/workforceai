-- Company edition (D4): a department shares a file or folder of its drive folder with another department, read only,
-- with the approval of a responsible of the owner department (docs/superpowers/plans/2026-11-15-d4-entre-departamentos.md).
-- A share is a request: pending until a decider approves (active) or rejects it; an active one can be revoked.
-- Additive and idempotent. Down: backend/migrations/down/821_drive_office_shares_down.sql.

CREATE TABLE IF NOT EXISTS drive_office_shares (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id               TEXT NOT NULL,
    path             TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('file', 'folder')),
    file_id          TEXT NOT NULL DEFAULT '', -- id of the shared file, or of the shared folder
    owner_office_id  TEXT NOT NULL,
    target_office_id TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'active', 'rejected', 'revoked')),
    requested_by     TEXT NOT NULL,
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by       TEXT NOT NULL DEFAULT '',
    decided_at       TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS drive_office_shares_target_idx ON drive_office_shares (org_id, target_office_id, status);
CREATE INDEX IF NOT EXISTS drive_office_shares_owner_idx ON drive_office_shares (org_id, owner_office_id, status);

ALTER TABLE drive_office_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE drive_office_shares FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON drive_office_shares;
CREATE POLICY tenant_isolation ON drive_office_shares
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON drive_office_shares TO app_user;
        REVOKE DELETE ON drive_office_shares FROM app_user;
    END IF;
END
$$;
