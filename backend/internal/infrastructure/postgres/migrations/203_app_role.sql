-- Application role for Row-Level Security.
--
-- app_user is NOT a superuser, does NOT have BYPASSRLS and owns no table, so the
-- policies of 202_rls_policies.sql always apply to it. The server connects with
-- the owner/DDL credentials and runs `SET ROLE app_user` on every pooled
-- connection (DB_APP_ROLE); alternatively give a login role membership in
-- app_user (GRANT app_user TO my_login_role) and point DATABASE_URL at it, with
-- MIGRATE_DATABASE_URL holding the owner credentials.
--
-- Idempotent. Grants target the schema this migration runs in.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
    END IF;
END
$$;

DO $$
DECLARE
    sch TEXT := current_schema();
BEGIN
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO app_user', sch);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %I TO app_user', sch);
    EXECUTE format('GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %I TO app_user', sch);
    -- Future tables created by the migrating role (later migrations).
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user', sch);
    -- The migration ledger is not the application's business.
    EXECUTE format('REVOKE ALL ON TABLE %I.schema_migrations FROM app_user', sch);
    -- Audit trail is append-only for the application.
    EXECUTE format('REVOKE UPDATE, DELETE ON TABLE %I.audit_logs FROM app_user', sch);
END
$$;
