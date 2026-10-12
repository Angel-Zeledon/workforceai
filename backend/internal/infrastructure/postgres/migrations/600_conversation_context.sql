-- Conversation context for the slash commands /clear and /compact (K1).
-- Spec: docs/architecture/commands.md.
--
-- A chat conversation has a fixed id ('office', 'agent:<id>'), so "a new
-- conversation" is an EPOCH: messages at or before cleared_at / summary_upto stay
-- in the history (nothing is deleted) but are not sent to the model any more;
-- `summary` (written by /compact) is sent instead of them.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql (RLS by org_id).

CREATE TABLE IF NOT EXISTS conversation_contexts (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL,
    epoch           INT NOT NULL DEFAULT 0,
    cleared_at      TIMESTAMPTZ,                       -- NULL = never cleared
    summary         TEXT NOT NULL DEFAULT '',
    summary_upto    TIMESTAMPTZ,                       -- NULL = no summary yet
    summary_tokens  INT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, conversation_id)
);

DO $$
BEGIN
    ALTER TABLE conversation_contexts ENABLE ROW LEVEL SECURITY;
    ALTER TABLE conversation_contexts FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON conversation_contexts;
    CREATE POLICY tenant_isolation ON conversation_contexts USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON conversation_contexts TO app_user;
    END IF;
END
$$;
