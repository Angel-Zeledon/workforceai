-- Tenant data lifecycle (GDPR): scheduled deletion of an organization, its purge, and a tombstone.
-- Code and documentation: internal/tenantdata (README.md there).
--
--   organizations.pending_deletion_at  when the owner asked for the deletion (NULL = no deletion pending)
--   organizations.deletion_at          when the purge becomes due (request + 7 days grace)
--   organizations.deletion_requested_by user id of the owner who asked
--
-- While a deletion is pending nothing else changes: the organization keeps working and the app shows a banner.
-- Once deletion_at has passed the janitor (tenantdata.Janitor) purges every row of the organization in one
-- transaction on the owner connection, then writes one non-personal row in org_tombstones.
--
-- Append-only tables (audit_logs, artifact_versions, drive_versions, skill_versions) refuse DELETE through
-- triggers. A purge sets the transaction-local setting app.purge_org to the organization being purged; the
-- triggers then let exactly that organization's rows go. The application role (app_user) has no DELETE
-- privilege on any of these tables (203/250/280/480/510), so it cannot use this door: only the owner
-- connection of the janitor can. Any new append-only trigger must honor app_purging_org() as well
-- (the Postgres purge test fails otherwise).
--
-- The demo organization can never be deleted: a trigger refuses it, which aborts the whole purge transaction.
--
-- Idempotent.

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS pending_deletion_at   TIMESTAMPTZ;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS deletion_at           TIMESTAMPTZ;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS deletion_requested_by TEXT;
CREATE INDEX IF NOT EXISTS organizations_deletion_due_idx ON organizations (deletion_at) WHERE deletion_at IS NOT NULL;

-- One row per purged organization: a SHA-256 of its id (no name, no e-mail, no personal data), the reason and
-- when. Lets operators answer "was this tenant deleted, when?" and spot abuse (re-creation) without keeping data.
-- Deliberately without org_id: it is not tenant data and survives the purge.
CREATE TABLE IF NOT EXISTS org_tombstones (
    org_hash   TEXT PRIMARY KEY,                    -- hex sha256 of the organization id
    reason     TEXT NOT NULL,                       -- 'requested' | 'trial_expired'
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        -- The application never reads or writes tombstones; only the janitor (owner connection) does.
        REVOKE ALL ON org_tombstones FROM app_user;
    END IF;
END
$$;

-- True when the current transaction is purging organization org (set_config('app.purge_org', org, true)).
CREATE OR REPLACE FUNCTION app_purging_org(org TEXT) RETURNS BOOLEAN
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT org IS NOT NULL AND org = NULLIF(current_setting('app.purge_org', true), '') $$;

CREATE OR REPLACE FUNCTION audit_logs_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND app_purging_org(OLD.org_id) THEN
        RETURN OLD; -- tenant purge (owner connection only: app_user has no DELETE on audit_logs)
    END IF;
    RAISE EXCEPTION 'audit_logs is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

CREATE OR REPLACE FUNCTION artifact_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND (pg_trigger_depth() > 1 OR app_purging_org(OLD.org_id)) THEN
        RETURN OLD; -- cascade from a deleted artifact, or a tenant purge
    END IF;
    RAISE EXCEPTION 'artifact_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

CREATE OR REPLACE FUNCTION drive_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND (pg_trigger_depth() > 1 OR app_purging_org(OLD.org_id)) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'drive_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

CREATE OR REPLACE FUNCTION skill_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND (pg_trigger_depth() > 1 OR app_purging_org(OLD.org_id)) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'skill_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

-- The demo organization (domain.DemoOrgID) is never deleted, whoever asks.
CREATE OR REPLACE FUNCTION organizations_protect_demo() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.id = '00000000-0000-0000-0000-000000000001' THEN
        RAISE EXCEPTION 'the demo organization cannot be deleted' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN OLD;
END
$$;

DROP TRIGGER IF EXISTS organizations_no_demo_delete ON organizations;
CREATE TRIGGER organizations_no_demo_delete BEFORE DELETE ON organizations
    FOR EACH ROW EXECUTE FUNCTION organizations_protect_demo();
