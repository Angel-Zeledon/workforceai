-- Corporate organization model, part 4: delegation rules (who may assign work to whom, who approves).
-- from/to are selectors: type 'any' | 'any_person' | 'any_agent' | 'person' | 'agent' | 'department' (+ id).
-- effect allow|deny; require_approval + approver (person/agent; empty = the head of the target's department).
-- The most specific matching rule wins (internal/orgchart/delegation.go); no rule at all keeps today's
-- behaviour (allowed). Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS delegation_rules (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id               TEXT NOT NULL,
    from_type        TEXT NOT NULL CHECK (from_type IN ('any', 'any_person', 'any_agent', 'person', 'agent', 'department')),
    from_id          TEXT NOT NULL DEFAULT '',
    to_type          TEXT NOT NULL CHECK (to_type IN ('any', 'any_person', 'any_agent', 'person', 'agent', 'department')),
    to_id            TEXT NOT NULL DEFAULT '',
    effect           TEXT NOT NULL DEFAULT 'allow' CHECK (effect IN ('allow', 'deny')),
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    approver_kind    TEXT NOT NULL DEFAULT '' CHECK (approver_kind IN ('', 'person', 'agent')),
    approver_id      TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['delegation_rules']
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
