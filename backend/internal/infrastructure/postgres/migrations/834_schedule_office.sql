-- Herencia de oficina (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §5): a recurring task
-- (schedule) created from a request or a conversation asked from an office remembers it, so every request it starts is
-- asked from that office. NULL = none (the agents' base offices, and everything created before this). Additive and
-- idempotent; RLS is untouched (the column lives in a table that already has its tenant policy).
-- Down: backend/migrations/down/834_schedule_office_down.sql.

ALTER TABLE schedules ADD COLUMN IF NOT EXISTS office_id TEXT;
