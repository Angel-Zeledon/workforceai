-- (Was 490 on its branch; renumbered at integration: 490 is the code sandbox.)
-- Chat files: a message may point to drive files (what an agent produced during
-- a task, or what the user attached). References only (path, file id, version),
-- never content. Spec: docs/architecture/drive.md (chat files).
-- Idempotent. Tenant isolation is unchanged (RLS by org_id on messages).

ALTER TABLE messages ADD COLUMN IF NOT EXISTS files JSONB NOT NULL DEFAULT '[]';
