-- Aislamiento por oficina (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §4, §5, §8):
-- messages, requests and tasks remember the office the work was asked from. NULL = General / unknown (everything written
-- before this change): it stays in General, and an agent of unknown scope only ever uses its base office.
-- No foreign key (a closed office keeps its history, like agent_offices). Additive and idempotent. RLS is untouched:
-- the columns live in tables that already have their tenant policy. Down: backend/migrations/down/833_office_scope_down.sql.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS office_id TEXT;
ALTER TABLE requests ADD COLUMN IF NOT EXISTS office_id TEXT;
ALTER TABLE tasks    ADD COLUMN IF NOT EXISTS office_id TEXT;
CREATE INDEX IF NOT EXISTS messages_office_idx ON messages (org_id, office_id) WHERE office_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS requests_office_idx ON requests (org_id, office_id) WHERE office_id IS NOT NULL;
