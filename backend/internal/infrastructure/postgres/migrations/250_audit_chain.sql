-- Tamper-evident audit trail and multi-approver approvals.
-- Spec: docs/architecture/07-seguridad-costos.md section 6, 06-permisos-autonomia.md
-- section 3.2 and 08-api.md (audit, approvals).
--
-- audit_logs (created in 001, RLS in 202, REVOKE UPDATE/DELETE in 203) gains the
-- hash chain: every entry of an organization carries seq (1, 2, 3...), the hash
-- of the previous entry and its own hash. Rows written before this migration
-- have seq NULL (not chained) and are ignored by the verification.
--
-- audit_chain_heads keeps the last (seq, hash) per organization: it serializes
-- appends (SELECT ... FOR UPDATE) and lets the verification detect that the tail
-- of the chain was removed. A trigger only allows it to advance by exactly one.
--
-- Append-only is enforced twice: privileges (the application role cannot
-- UPDATE/DELETE) and triggers (nobody can UPDATE, DELETE or TRUNCATE
-- audit_logs without first dropping the trigger, which is a visible DDL act).
--
-- approvals gains what double approval needs: how many humans must approve,
-- the minimum role, who asked, and the decisions received so far.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS seq        BIGINT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash  TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS hash       TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS audit_org_seq_uidx     ON audit_logs (org_id, seq) WHERE seq IS NOT NULL;
CREATE INDEX        IF NOT EXISTS audit_org_ts_id_idx    ON audit_logs (org_id, ts, id);
CREATE INDEX        IF NOT EXISTS audit_org_actor_idx    ON audit_logs (org_id, actor, ts);
CREATE INDEX        IF NOT EXISTS audit_org_action_idx   ON audit_logs (org_id, action text_pattern_ops);
CREATE INDEX        IF NOT EXISTS audit_org_request_idx  ON audit_logs (org_id, request_id) WHERE request_id <> '';

CREATE TABLE IF NOT EXISTS audit_chain_heads (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    seq        BIGINT NOT NULL DEFAULT 0,
    hash       TEXT NOT NULL DEFAULT '',
    last_ts    TIMESTAMPTZ,            -- timestamp of the last entry: timestamps strictly increase along the chain
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE audit_chain_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_chain_heads FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_chain_heads;
CREATE POLICY tenant_isolation ON audit_chain_heads
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

CREATE OR REPLACE FUNCTION audit_logs_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

DROP TRIGGER IF EXISTS audit_logs_no_change ON audit_logs;
CREATE TRIGGER audit_logs_no_change BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
DROP TRIGGER IF EXISTS audit_logs_no_truncate ON audit_logs;
CREATE TRIGGER audit_logs_no_truncate BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION audit_logs_append_only();

CREATE OR REPLACE FUNCTION audit_heads_monotonic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.org_id <> OLD.org_id OR NEW.seq <> OLD.seq + 1 THEN
        RAISE EXCEPTION 'audit chain head can only advance by one' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS audit_heads_advance_only ON audit_chain_heads;
CREATE TRIGGER audit_heads_advance_only BEFORE UPDATE ON audit_chain_heads
    FOR EACH ROW EXECUTE FUNCTION audit_heads_monotonic();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON audit_chain_heads TO app_user;
        REVOKE DELETE, TRUNCATE ON audit_chain_heads FROM app_user;
    END IF;
END
$$;

ALTER TABLE approvals ADD COLUMN IF NOT EXISTS required_approvals INT     NOT NULL DEFAULT 1 CHECK (required_approvals BETWEEN 1 AND 2);
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS required_role      TEXT    NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS requested_by       TEXT    NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS no_self_approval   BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS policy_rule        TEXT    NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS decisions          JSONB   NOT NULL DEFAULT '[]';
