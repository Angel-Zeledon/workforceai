-- Auto-join by verified domain (docs/architecture/identity.md section 3).
--
-- org_auto_join: one row per organization that has touched the setting. enabled is false by default: nobody joins an
-- organization just because of the domain of their e-mail address until an owner or admin turns this on. role is what a
-- person who joins this way receives (never owner; viewer by default, the least privilege).
-- The domain must be verified by the organization (verified_domains) and the person must have proven the address
-- (verification link, or the identity provider's verified claim).
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS org_auto_join (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled    BOOLEAN NOT NULL DEFAULT false,
    role       TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'member', 'viewer')),
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE org_auto_join ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_auto_join FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON org_auto_join;
CREATE POLICY tenant_isolation ON org_auto_join
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON org_auto_join TO app_user;
    END IF;
END
$$;
