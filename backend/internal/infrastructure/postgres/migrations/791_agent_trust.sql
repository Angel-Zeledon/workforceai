-- Trust ladder (docs/architecture/trust-ladder.md, feature B1): autonomy earned, not switched on.
--
-- The evidence itself is read from tables that already exist (approvals, tasks, audit_logs); only the ladder's own
-- state is new: one record per agent (when tracking started, the level it started at, since when it holds its
-- current level) and the proposals (suggested promotions/demotions, and automatic demotions) with the evidence
-- snapshot behind each one. Proposals have no foreign key to agents: the history stays even if an agent is removed.
-- agents.autonomy is still the only source of truth for the level. Idempotent. Tenant isolation follows
-- 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS agent_trust (
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id       TEXT NOT NULL,
    start_level    TEXT NOT NULL,
    level          TEXT NOT NULL,
    level_since    TIMESTAMPTZ NOT NULL DEFAULT now(),
    tracking_since TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);

CREATE TABLE IF NOT EXISTS trust_proposals (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    agent_id    TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('promotion', 'demotion')),
    from_level  TEXT NOT NULL,
    to_level    TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'applied', 'reverted', 'superseded')),
    auto        BOOLEAN NOT NULL DEFAULT false,
    reasons     JSONB NOT NULL DEFAULT '[]',
    evidence    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS trust_proposals_agent_idx ON trust_proposals (org_id, agent_id, created_at DESC);
-- at most one proposal waits for a human per agent
CREATE UNIQUE INDEX IF NOT EXISTS trust_proposals_one_pending ON trust_proposals (org_id, agent_id) WHERE status = 'pending';

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_trust', 'trust_proposals']
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
