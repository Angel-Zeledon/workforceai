-- Agentes en varias oficinas (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §5 and §7):
-- an agent keeps ONE base office (agent_access / department, offices.Model.effective) and may also work for others.
-- One row per (agent, extra office): granted_by is who leads the base office and offered it; accepted_by is who leads
-- the other office and accepted ('' = pending, the members of that office cannot use the agent yet).
--
-- No foreign key to offices (a closed office keeps its rows, like office_positions); the model ignores rows whose
-- office or agent is gone. Additive and idempotent. Tenant isolation follows 202_rls_policies.sql. Down:
-- backend/migrations/down/832_agent_offices_down.sql.

CREATE TABLE IF NOT EXISTS agent_offices (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    office_id   TEXT NOT NULL,
    granted_by  TEXT NOT NULL DEFAULT '',
    accepted_by TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, agent_id, office_id)
);
CREATE INDEX IF NOT EXISTS agent_offices_office_idx ON agent_offices (org_id, office_id);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_offices'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
