-- F4 "Historial del agente" (docs/architecture/agent-history.md): every change of an agent's configuration, by any
-- route, leaves an immutable version with a full snapshot (JSONB, no secrets: a secret field only keeps a
-- fingerprint), who made it (a person, or the system for automatic changes such as a demotion after an incident),
-- when, how it arrived (source) and which fields changed. Restoring a version writes a NEW version (restored_from).
--
-- Append-only: the application role may insert, read and delete (the retention keeps the newest 100 per agent), but
-- never update; a trigger refuses any UPDATE, also for the owner. Additive and idempotent. Tenant isolation follows
-- 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS agent_config_versions (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    version       INTEGER NOT NULL CHECK (version > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor         TEXT NOT NULL DEFAULT 'system',
    actor_kind    TEXT NOT NULL DEFAULT 'system' CHECK (actor_kind IN ('person', 'system')),
    actor_name    TEXT NOT NULL DEFAULT '' CHECK (char_length(actor_name) <= 120),
    source        TEXT NOT NULL DEFAULT '' CHECK (char_length(source) <= 120),
    fields        JSONB NOT NULL DEFAULT '[]'::jsonb,
    restored_from INTEGER,
    config        JSONB NOT NULL,
    PRIMARY KEY (org_id, agent_id, version)
);
CREATE INDEX IF NOT EXISTS agent_config_versions_recent_idx ON agent_config_versions (org_id, agent_id, version DESC);

CREATE OR REPLACE FUNCTION agent_config_versions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'agent_config_versions is append-only';
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'agent_config_versions_no_update') THEN
        CREATE TRIGGER agent_config_versions_no_update BEFORE UPDATE ON agent_config_versions
            FOR EACH ROW EXECUTE FUNCTION agent_config_versions_immutable();
    END IF;
    ALTER TABLE agent_config_versions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE agent_config_versions FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON agent_config_versions;
    CREATE POLICY tenant_isolation ON agent_config_versions USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, DELETE ON agent_config_versions TO app_user;
        REVOKE UPDATE ON agent_config_versions FROM app_user;
    END IF;
END
$$;
