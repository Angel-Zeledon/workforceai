-- Controls: global kill switch, read-only mode, per-tool kill switch, agent
-- pause and governance settings (plan review).
-- Spec: docs/architecture/integrations-credentials.md section 14.
--
-- agent_controls is a separate table (instead of new columns on agents) so the
-- existing agent code and contract are untouched: no row = active agent.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_controls (
    org_id            TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    mode              TEXT NOT NULL DEFAULT 'normal' CHECK (mode IN ('normal', 'read_only')),
    kill_switch_level TEXT NOT NULL DEFAULT 'none' CHECK (kill_switch_level IN ('none', 'freeze', 'lockdown')),
    reason            TEXT NOT NULL DEFAULT '',
    set_by            TEXT NOT NULL DEFAULT '',
    set_at            TIMESTAMPTZ,
    settings          JSONB NOT NULL DEFAULT '{}',   -- {"plan_review": "touches_writes"}
    disabled_tools    TEXT[] NOT NULL DEFAULT '{}'   -- per-tool kill switch: "email" or "email.send"
);

CREATE TABLE IF NOT EXISTS agent_controls (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL,
    control      TEXT NOT NULL DEFAULT 'active' CHECK (control IN ('active', 'paused')),
    drain        TEXT NOT NULL DEFAULT '' CHECK (drain IN ('', 'graceful', 'immediate')),
    read_only    BOOLEAN NOT NULL DEFAULT false,
    paused_by    TEXT NOT NULL DEFAULT '',
    paused_at    TIMESTAMPTZ,
    pause_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, agent_id)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_controls', 'agent_controls']
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
