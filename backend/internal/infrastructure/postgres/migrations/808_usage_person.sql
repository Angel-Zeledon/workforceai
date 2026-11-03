-- E0: the cost ledger knows the person behind every call they started (chat replies, the requests they
-- asked for). '' = no person (scheduled work, the system, entries written before this migration).
-- GET /governance/reports/usage?by=person groups by it. Additive and idempotent.
ALTER TABLE usage_entries ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS usage_entries_org_user_idx ON usage_entries (org_id, user_id, ts DESC);
