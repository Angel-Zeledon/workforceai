-- Invitations to join an organization (A2, docs/architecture/08-api.md).
--
-- Only the SHA-256 of the invitation token is stored; the plaintext is shown
-- once to the inviter. Revocation and acceptance are timestamps, so history is
-- kept (the application role cannot DELETE).
--
-- Tenant isolation follows 202_rls_policies.sql with one addition: accepting an
-- invitation happens before the tenant is known, so a transaction that sets
-- app.invitation_hash can see and update the single row with that hash and
-- nothing else. Knowing the hash requires knowing the 256-bit token.
--
-- Idempotent.

CREATE TABLE IF NOT EXISTS invitations (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       TEXT NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    role        TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    token_hash  TEXT NOT NULL UNIQUE,
    invited_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by TEXT,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS invitations_org_idx ON invitations (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS invitations_pending_idx ON invitations (org_id, lower(email))
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON invitations;
CREATE POLICY tenant_isolation ON invitations
    USING      (org_id = app_current_org() OR token_hash = NULLIF(current_setting('app.invitation_hash', true), ''))
    WITH CHECK (org_id = app_current_org() OR token_hash = NULLIF(current_setting('app.invitation_hash', true), ''));

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON invitations TO app_user;
        REVOKE DELETE ON invitations FROM app_user;
    END IF;
END
$$;
