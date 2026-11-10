-- Company edition (D3): a connection may belong to a department (office). '' = General (the whole company, as
-- before). Only the agents of that office may be granted it, the gateway refuses an old grant of an agent of another
-- office, and only its members see it when the office is private. Additive and idempotent. Down:
-- backend/migrations/down/820_connections_office_down.sql.

ALTER TABLE connections ADD COLUMN IF NOT EXISTS office_id TEXT NOT NULL DEFAULT '';
