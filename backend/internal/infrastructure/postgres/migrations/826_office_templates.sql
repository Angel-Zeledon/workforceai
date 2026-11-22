-- F3 "Guardar un departamento como plantilla y llevarlo a otra empresa" (docs/architecture/office-templates.md): the
-- office templates imported into an organization. Each row is a validated template file (aiw.office-template), kept
-- whole in `body` so it can be previewed and installed again; `fingerprint` makes importing the same content twice
-- keep one copy. A template never carries ids, connections, credentials, memory, files, people or customers: the
-- allow-list is enforced by the service before the row is written.
--
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS office_templates (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    kind        TEXT NOT NULL CHECK (kind IN ('department', 'company')),
    fingerprint TEXT NOT NULL,
    body        JSONB NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, fingerprint),
    CHECK (octet_length(body::text) <= 4194304)
);
CREATE INDEX IF NOT EXISTS office_templates_created_idx ON office_templates (org_id, created_at DESC);

DO $$
BEGIN
    ALTER TABLE office_templates ENABLE ROW LEVEL SECURITY;
    ALTER TABLE office_templates FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON office_templates;
    CREATE POLICY tenant_isolation ON office_templates USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, DELETE ON office_templates TO app_user;
    END IF;
END
$$;
