-- Rollback of 250_audit_chain.sql. Dropping the hash columns discards the chain:
-- export the audit trail first if it must be kept for verification.
ALTER TABLE approvals DROP COLUMN IF EXISTS decisions;
ALTER TABLE approvals DROP COLUMN IF EXISTS policy_rule;
ALTER TABLE approvals DROP COLUMN IF EXISTS no_self_approval;
ALTER TABLE approvals DROP COLUMN IF EXISTS requested_by;
ALTER TABLE approvals DROP COLUMN IF EXISTS required_role;
ALTER TABLE approvals DROP COLUMN IF EXISTS required_approvals;

DROP TRIGGER IF EXISTS audit_heads_advance_only ON audit_chain_heads;
DROP TABLE IF EXISTS audit_chain_heads;
DROP FUNCTION IF EXISTS audit_heads_monotonic();

DROP TRIGGER IF EXISTS audit_logs_no_truncate ON audit_logs;
DROP TRIGGER IF EXISTS audit_logs_no_change ON audit_logs;
DROP FUNCTION IF EXISTS audit_logs_append_only();

DROP INDEX IF EXISTS audit_org_request_idx;
DROP INDEX IF EXISTS audit_org_action_idx;
DROP INDEX IF EXISTS audit_org_actor_idx;
DROP INDEX IF EXISTS audit_org_ts_id_idx;
DROP INDEX IF EXISTS audit_org_seq_uidx;

ALTER TABLE audit_logs DROP COLUMN IF EXISTS request_id;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS hash;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS prev_hash;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS seq;
