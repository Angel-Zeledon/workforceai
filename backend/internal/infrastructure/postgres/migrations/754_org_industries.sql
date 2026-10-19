-- Corporate organization model, part 5: which industry templates were applied (so applying twice never
-- duplicates the sample projects). Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_industries (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    industry_key TEXT NOT NULL,
    version      INT  NOT NULL DEFAULT 1,
    locale       TEXT NOT NULL DEFAULT 'es',
    applied_by   TEXT NOT NULL DEFAULT '',
    applied_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    detail       JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (org_id, industry_key)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_industries']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
