-- Office editor: one layout document per organization (wall/floor colors, light, workstations and the freely
-- placed furniture), replaced as a whole and versioned for optimistic concurrency. Spec:
-- docs/architecture/office-editor.md. The version is bumped by a compare-and-set UPDATE (internal/rewards).
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS office_layout (
    org_id     TEXT  PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    doc        JSONB NOT NULL,
    version    INT   NOT NULL CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT  NOT NULL DEFAULT ''
);

ALTER TABLE office_layout ENABLE ROW LEVEL SECURITY;
ALTER TABLE office_layout FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON office_layout;
CREATE POLICY tenant_isolation ON office_layout USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON office_layout TO app_user;
    END IF;
END
$$;
