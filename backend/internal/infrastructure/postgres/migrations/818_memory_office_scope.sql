-- D3 "sellar departamentos": what the agents of a department learn stays in the department. A memory document gets a
-- scope 'office' (besides 'org' and 'agent') and the office it belongs to; only the agents of that office recall it.
-- General's agents keep writing scope 'org'; every document that exists today keeps its scope, so nothing disappears
-- from anyone's memory. The scope CHECK is the unnamed one of 550_memory.sql (memory_docs_scope_check).
--
-- Additive and idempotent. memory_docs already has org RLS (550_memory.sql). Down:
-- backend/migrations/down/818_memory_office_scope_down.sql.

ALTER TABLE memory_docs ADD COLUMN IF NOT EXISTS office_id TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_docs DROP CONSTRAINT IF EXISTS memory_docs_scope_check;
ALTER TABLE memory_docs ADD CONSTRAINT memory_docs_scope_check CHECK (scope IN ('org','agent','office'));
