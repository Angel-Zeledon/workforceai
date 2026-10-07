-- Rollback of 203_app_role.sql. The role is cluster-wide and may be shared by
-- other schemas, so it is only stripped of privileges here, not dropped.
DO $$
DECLARE
    sch TEXT := current_schema();
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I REVOKE ALL ON TABLES FROM app_user', sch);
        EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM app_user', sch);
        EXECUTE format('REVOKE ALL ON ALL FUNCTIONS IN SCHEMA %I FROM app_user', sch);
        EXECUTE format('REVOKE USAGE ON SCHEMA %I FROM app_user', sch);
    END IF;
END
$$;
