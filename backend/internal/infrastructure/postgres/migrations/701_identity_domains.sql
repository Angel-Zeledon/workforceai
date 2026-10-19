-- Enterprise identity, part 2: verified e-mail domains (DNS TXT challenge). Idempotent.
--
-- org_domains is tenant data (RLS). verified_domains is the pre-tenant lookup behind "sign in with SSO": at that point
-- the organization is not known yet, so it cannot sit behind a tenant policy. It holds no secret (domain -> org id) and
-- is written only by internal/sso, in the same transaction that marks the domain verified. The primary key on `domain`
-- is what makes a domain verifiable by one organization at a time.
CREATE TABLE IF NOT EXISTS org_domains (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain        TEXT NOT NULL CHECK (domain = lower(domain) AND char_length(domain) BETWEEN 3 AND 253),
    token         TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at   TIMESTAMPTZ,
    last_check_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, domain)
);

CREATE TABLE IF NOT EXISTS verified_domains (
    domain      TEXT PRIMARY KEY CHECK (domain = lower(domain)),
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS verified_domains_org_idx ON verified_domains (org_id);

ALTER TABLE org_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_domains FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON org_domains;
CREATE POLICY tenant_isolation ON org_domains
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON org_domains TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON verified_domains TO app_user;
    END IF;
END
$$;
