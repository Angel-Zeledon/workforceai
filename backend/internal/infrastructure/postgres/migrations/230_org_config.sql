-- Organization configuration (improvements #4, #10 and part of #14 of docs/competitive-analysis.md).
--
-- org_settings:   one row per organization: language, regional tone, the business
--                 type chosen in the first-run wizard and the seeded rules.
-- agent_settings: optional per-agent regional tone override.
-- schedules:      simple recurring tasks ("daily briefing at 8:00"): run a workflow
--                 template at HH:MM in a time zone on the given weekdays.
--
-- No row in org_settings means "nothing configured": the backend then sends no
-- locale/tone to the runtime and behaves exactly as before.
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_settings (
    org_id                  TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    locale                  TEXT NOT NULL DEFAULT 'es' CHECK (locale IN ('es', 'en')),
    tone                    TEXT NOT NULL DEFAULT 'neutral' CHECK (tone IN ('neutral', 'mx', 'co', 'ar', 'cl', 'es')),
    business_type           TEXT NOT NULL DEFAULT '',
    pack_version            INT NOT NULL DEFAULT 0,
    onboarding_completed_at TIMESTAMPTZ,
    rules                   JSONB NOT NULL DEFAULT '{}',
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_settings (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    tone       TEXT NOT NULL CHECK (tone IN ('neutral', 'mx', 'co', 'ar', 'cl', 'es')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);

CREATE TABLE IF NOT EXISTS schedules (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    template_key TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    hour         SMALLINT NOT NULL CHECK (hour BETWEEN 0 AND 23),
    minute       SMALLINT NOT NULL CHECK (minute BETWEEN 0 AND 59),
    timezone     TEXT NOT NULL DEFAULT 'UTC',
    weekdays     JSONB NOT NULL DEFAULT '[]',
    enabled      BOOLEAN NOT NULL DEFAULT true,
    next_run_at  TIMESTAMPTZ,
    last_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS schedules_org_idx ON schedules (org_id, created_at);
CREATE INDEX IF NOT EXISTS schedules_due_idx ON schedules (next_run_at) WHERE enabled;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_settings', 'agent_settings', 'schedules']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
        -- 203_app_role.sql only grants on tables that existed when it ran.
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
