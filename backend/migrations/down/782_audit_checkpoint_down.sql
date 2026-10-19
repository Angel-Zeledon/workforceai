-- Rollback of 782_audit_checkpoint.sql. WARNING: if entries were already purged, the previous audit.Verify will report the
-- chain as broken (it starts at seq 1). Keep the columns if any organization has purged audit entries.
DROP FUNCTION IF EXISTS audit_purge_through(TEXT, BIGINT);
CREATE OR REPLACE FUNCTION audit_heads_monotonic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.org_id <> OLD.org_id OR NEW.seq <> OLD.seq + 1 THEN
        RAISE EXCEPTION 'audit chain head can only advance by one' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;
ALTER TABLE audit_chain_heads DROP COLUMN IF EXISTS checkpoint_seq;
ALTER TABLE audit_chain_heads DROP COLUMN IF EXISTS checkpoint_hash;
ALTER TABLE audit_chain_heads DROP COLUMN IF EXISTS checkpoint_at;
ALTER TABLE audit_chain_heads DROP COLUMN IF EXISTS purged_total;
