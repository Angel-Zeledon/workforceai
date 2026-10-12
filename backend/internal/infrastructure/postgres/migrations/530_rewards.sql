-- Coin system (C1): an append-only coin ledger, the running wallet of each
-- organization, the items it owns and where they are equipped/placed.
-- Coins are earned only by finished work (internal/rewards); nothing here can
-- be topped up from outside. Spec: docs/architecture/rewards.md.
--
-- coin_ledger is append-only (no UPDATE/DELETE grant for the app role) and
-- idempotent: (org_id, idem_key) is unique, so a task pays once even after
-- retries, restarts and event replays. coin_wallets.balance can never go
-- below zero (CHECK) and its row lock serializes the purchases of one org.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS coin_wallets (
    org_id       TEXT   PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    balance      BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    earned_total BIGINT NOT NULL DEFAULT 0 CHECK (earned_total >= 0),
    spent_total  BIGINT NOT NULL DEFAULT 0 CHECK (spent_total >= 0),
    entries      BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS coin_ledger (
    id            TEXT   PRIMARY KEY,
    org_id        TEXT   NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    seq           BIGINT NOT NULL,
    kind          TEXT   NOT NULL CHECK (kind IN ('earn','spend')),
    amount        BIGINT NOT NULL CHECK (amount > 0),
    reason        TEXT   NOT NULL,
    ref_kind      TEXT   NOT NULL DEFAULT '',
    ref_id        TEXT   NOT NULL DEFAULT '',
    title         TEXT   NOT NULL DEFAULT '',
    agent_id      TEXT   NOT NULL DEFAULT '',
    actor         TEXT   NOT NULL DEFAULT 'system',
    detail        JSONB  NOT NULL DEFAULT '{}',
    balance_after BIGINT NOT NULL CHECK (balance_after >= 0),
    idem_key      TEXT   NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, idem_key),
    UNIQUE (org_id, seq)
);
CREATE INDEX IF NOT EXISTS coin_ledger_agent_idx ON coin_ledger (org_id, agent_id) WHERE kind = 'earn';

CREATE TABLE IF NOT EXISTS shop_inventory (
    org_id       TEXT   NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    item_id      TEXT   NOT NULL,
    price_paid   BIGINT NOT NULL CHECK (price_paid >= 0),
    purchased_by TEXT   NOT NULL DEFAULT '',
    purchased_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, item_id)
);

CREATE TABLE IF NOT EXISTS shop_equipment (
    org_id   TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    slot     TEXT NOT NULL,
    item_id  TEXT NOT NULL,
    PRIMARY KEY (org_id, agent_id, slot)
);

CREATE TABLE IF NOT EXISTS shop_office (
    org_id  TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slot    TEXT NOT NULL,
    item_id TEXT NOT NULL,
    PRIMARY KEY (org_id, slot)
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['coin_wallets','coin_ledger','shop_inventory','shop_equipment','shop_office'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON coin_wallets TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON shop_equipment, shop_office TO app_user;
        -- Append-only for the application (default privileges of 203 grant everything).
        REVOKE UPDATE, DELETE ON coin_ledger, shop_inventory FROM app_user;
        REVOKE DELETE ON coin_wallets FROM app_user;
    END IF;
END
$$;
