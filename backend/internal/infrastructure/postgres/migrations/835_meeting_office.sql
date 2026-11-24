-- Herencia de oficina (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §5): a meeting created
-- from a request or a conversation asked from an office remembers it, so the requests its tasks start are asked from
-- that office. NULL = none (the agents' base offices, and everything created before this). Additive and idempotent;
-- RLS is untouched (the column lives in a table that already has its tenant policy).
-- Down: backend/migrations/down/835_meeting_office_down.sql.

ALTER TABLE meetings ADD COLUMN IF NOT EXISTS office_id TEXT;
