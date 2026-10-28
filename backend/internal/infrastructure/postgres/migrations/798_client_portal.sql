-- Client portal (docs/architecture/client-portal.md, feature B13): the firm's branding and the read-only
-- share links of a project.
--
-- portal_branding: one row per organization (display name, accent color, logo). The logo is stored after
--   it was decoded and re-encoded by the backend (PNG/JPEG, at most 512 px, 256 KiB), never as uploaded.
-- portal_links: a link shows ONE project. Only the SHA-256 of its token is stored; the plaintext is shown
--   once. Revocation, first and last view are timestamps, so history is kept (no DELETE for the app role).
--
-- Tenant isolation follows 202_rls_policies.sql with one addition, like 711_api_access.sql: the public page
-- opens before the tenant is known, so a read-only policy lets a transaction that set app.portal_hash see
-- the single link with that hash and nothing else. Knowing the hash requires knowing the 256-bit token.
-- Everything after that lookup (the view, the project, the branding) runs in a normal org-scoped
-- transaction. Idempotent.

CREATE TABLE IF NOT EXISTS portal_branding (
    org_id       TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 80),
    accent_color TEXT NOT NULL DEFAULT '' CHECK (accent_color = '' OR accent_color ~ '^#[0-9a-f]{6}$'),
    logo         BYTEA CHECK (logo IS NULL OR octet_length(logo) <= 262144),
    logo_mime    TEXT NOT NULL DEFAULT '' CHECK (logo_mime IN ('', 'image/png', 'image/jpeg')),
    updated_by   TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS portal_links (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    label           TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 80),
    title           TEXT NOT NULL DEFAULT '' CHECK (char_length(title) <= 120),
    token_hash      TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    deliverable_ids JSONB NOT NULL DEFAULT '[]',
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    revoked_by      TEXT NOT NULL DEFAULT '',
    first_viewed_at TIMESTAMPTZ,
    last_viewed_at  TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS portal_links_project_idx ON portal_links (org_id, project_id, created_at DESC);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['portal_branding', 'portal_links']
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
        GRANT SELECT, INSERT, UPDATE, DELETE ON portal_branding TO app_user;
        GRANT SELECT, INSERT, UPDATE ON portal_links TO app_user;
        REVOKE DELETE ON portal_links FROM app_user;
    END IF;
END
$$;

-- Token lookup: read-only, and only the row whose hash the transaction announced.
DROP POLICY IF EXISTS portal_lookup ON portal_links;
CREATE POLICY portal_lookup ON portal_links FOR SELECT
    USING (token_hash = NULLIF(current_setting('app.portal_hash', true), ''));
