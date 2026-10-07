-- Phase 2: Row-Level Security for every tenant table.
--
-- Contract: each request runs in a transaction that begins with
--     SELECT set_config('app.org_id', '<org id>', true);   -- == SET LOCAL app.org_id
-- (see auth.SetOrgID / auth.WithOrgTx). The setting is transaction-local, so a
-- pooled connection can never carry one tenant's id into another request.
--
-- Fail closed: when app.org_id is unset or empty, app_current_org() is NULL,
-- every comparison is NULL, and no row is visible or writable.
--
-- IMPORTANT:
--   * Superusers and roles with BYPASSRLS ignore RLS. The application must
--     connect as a plain role (e.g. `app_user`) that is NOT the table owner and
--     not a superuser. Keep a separate BYPASSRLS role for migrations/ops.
--   * FORCE ROW LEVEL SECURITY is enabled so even the table owner is filtered.
--   * Every query on these tables must run through WithOrgTx; the Postgres
--     store does so for each call (postgres.Store.WithOrgTx).
--   * 203_app_role.sql creates that plain role (app_user) and the server
--     switches to it on every connection (SET ROLE).
--
-- Idempotent: safe to re-run.

CREATE OR REPLACE FUNCTION app_current_org() RETURNS TEXT
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT NULLIF(current_setting('app.org_id', true), '') $$;

-- organizations is keyed by id (org_id is a generated copy of id).
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON organizations;
CREATE POLICY tenant_isolation ON organizations
    USING      (id = app_current_org())
    WITH CHECK (id = app_current_org());

-- Every other tenant table carries org_id.
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'agents', 'tasks', 'requests', 'conversations', 'messages',
        'approvals', 'reports', 'memories', 'events', 'audit_logs', 'activity'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
END
$$;

-- audit_logs is append-only for the application: see 203_app_role.sql
-- (REVOKE UPDATE, DELETE ON audit_logs FROM app_user).
