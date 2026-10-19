-- Audit retention with a hash-chain checkpoint (docs/architecture/governance.md, "Audit retention").
--
-- The audit trail stays append-only for the application role. The only way to remove old entries is
-- audit_purge_through(org, seq), a SECURITY DEFINER function that:
--   * works only for the organization of the current transaction (app_current_org()),
--   * never removes the head of the chain (the newest entry always stays),
--   * reads the hash of the last removed entry from the table itself (the caller cannot invent one),
--   * records it as the organization's CHECKPOINT (checkpoint_seq, checkpoint_hash) in audit_chain_heads, in the same
--     transaction as the delete, and counts the rows removed (purged_total).
-- audit.Verify starts at the checkpoint: the first remaining entry must have seq = checkpoint_seq + 1 and
-- prev_hash = checkpoint_hash, so editing, inserting, reordering or truncating what is left still breaks the chain.
-- The head trigger lets the checkpoint move forward only inside that function, and never during an append.
--
-- Idempotent.
ALTER TABLE audit_chain_heads ADD COLUMN IF NOT EXISTS checkpoint_seq  BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_chain_heads ADD COLUMN IF NOT EXISTS checkpoint_hash TEXT   NOT NULL DEFAULT '';
ALTER TABLE audit_chain_heads ADD COLUMN IF NOT EXISTS checkpoint_at   TIMESTAMPTZ;
ALTER TABLE audit_chain_heads ADD COLUMN IF NOT EXISTS purged_total    BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION audit_heads_monotonic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.org_id <> OLD.org_id THEN
        RAISE EXCEPTION 'audit chain head can only advance by one' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.seq = OLD.seq + 1 THEN
        -- an append never touches the checkpoint
        IF NEW.checkpoint_seq <> OLD.checkpoint_seq OR NEW.checkpoint_hash <> OLD.checkpoint_hash OR NEW.purged_total <> OLD.purged_total THEN
            RAISE EXCEPTION 'audit checkpoint cannot change during an append' USING ERRCODE = 'insufficient_privilege';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.seq = OLD.seq AND NEW.hash = OLD.hash AND NEW.checkpoint_seq > OLD.checkpoint_seq AND NEW.checkpoint_seq < OLD.seq
       AND NEW.org_id = NULLIF(current_setting('app.audit_checkpoint', true), '') THEN
        RETURN NEW; -- the checkpoint moves forward, only from audit_purge_through
    END IF;
    RAISE EXCEPTION 'audit chain head can only advance by one' USING ERRCODE = 'insufficient_privilege';
END
$$;

CREATE OR REPLACE FUNCTION audit_purge_through(p_org TEXT, p_through BIGINT) RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_head BIGINT;
    v_cp   BIGINT;
    v_hash TEXT;
    v_n    BIGINT;
BEGIN
    IF p_org IS NULL OR p_org IS DISTINCT FROM app_current_org() THEN
        RAISE EXCEPTION 'audit purge: wrong organization' USING ERRCODE = 'insufficient_privilege';
    END IF;
    SELECT seq, checkpoint_seq INTO v_head, v_cp FROM audit_chain_heads WHERE org_id = p_org FOR UPDATE;
    IF NOT FOUND OR p_through <= v_cp THEN
        RETURN 0;
    END IF;
    IF p_through >= v_head THEN
        RAISE EXCEPTION 'audit purge: the head of the chain is never removed' USING ERRCODE = 'insufficient_privilege';
    END IF;
    SELECT hash INTO v_hash FROM audit_logs WHERE org_id = p_org AND seq = p_through;
    IF v_hash IS NULL THEN
        RAISE EXCEPTION 'audit purge: seq % does not exist', p_through USING ERRCODE = 'insufficient_privilege';
    END IF;
    PERFORM set_config('app.purge_org', p_org, true);
    PERFORM set_config('app.audit_checkpoint', p_org, true);
    DELETE FROM audit_logs WHERE org_id = p_org AND seq IS NOT NULL AND seq <= p_through;
    GET DIAGNOSTICS v_n = ROW_COUNT;
    UPDATE audit_chain_heads SET checkpoint_seq = p_through, checkpoint_hash = v_hash, checkpoint_at = now(),
        purged_total = purged_total + v_n WHERE org_id = p_org;
    PERFORM set_config('app.purge_org', '', true);
    PERFORM set_config('app.audit_checkpoint', '', true);
    RETURN v_n;
END
$$;

REVOKE ALL ON FUNCTION audit_purge_through(TEXT, BIGINT) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT EXECUTE ON FUNCTION audit_purge_through(TEXT, BIGINT) TO app_user;
    END IF;
END
$$;
