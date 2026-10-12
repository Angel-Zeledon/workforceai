-- Standing per-project approvals (A1): "always allow this kind of action in
-- this project" for INTERNAL, REVERSIBLE actions only. A standing approval is
-- scoped to one project, one tool action and (by default) one agent, expires
-- (with the project, or after a configurable number of days) and is revocable
-- from the project screen. It is never created for external or irreversible
-- actions: the classification lives in internal/application/standing.go and is
-- enforced when the grant is created and again every time it is used.
-- Spec: docs/architecture/06-permisos-autonomia.md (standing approvals).
--
-- Rows are never deleted by the application: revoking sets revoked_at, so the
-- history stays for the audit trail.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS standing_approvals (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL,
    agent_id    TEXT NOT NULL DEFAULT '',   -- '' = any agent of the project
    tool        TEXT NOT NULL,
    action      TEXT NOT NULL,
    approval_id TEXT NOT NULL DEFAULT '',   -- the approval the person was deciding when they chose "always"
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    revoked_by  TEXT NOT NULL DEFAULT '',
    use_count   INT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ,
    CHECK (tool <> '' AND action <> '' AND project_id <> '')
);
CREATE INDEX IF NOT EXISTS standing_approvals_project_idx ON standing_approvals (org_id, project_id, created_at DESC);

-- What an approval is about, so the decision can offer (and the server can
-- re-check) the "always allow" choice.
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS tool              TEXT    NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS project_id        TEXT    NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS standing_eligible BOOLEAN NOT NULL DEFAULT false;

DO $$
BEGIN
    ALTER TABLE standing_approvals ENABLE ROW LEVEL SECURITY;
    ALTER TABLE standing_approvals FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON standing_approvals;
    CREATE POLICY tenant_isolation ON standing_approvals USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON standing_approvals TO app_user;
        -- Revoked rows stay as history.
        REVOKE DELETE ON standing_approvals FROM app_user;
    END IF;
END
$$;
