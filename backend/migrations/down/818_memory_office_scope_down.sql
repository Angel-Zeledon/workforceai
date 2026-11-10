-- Down of 818_memory_office_scope.sql: the documents of an office go back to organization knowledge (visible to
-- everyone, as before D3), then the column and the wider CHECK go away.
UPDATE memory_docs SET scope = 'org' WHERE scope = 'office';
ALTER TABLE memory_docs DROP CONSTRAINT IF EXISTS memory_docs_scope_check;
ALTER TABLE memory_docs ADD CONSTRAINT memory_docs_scope_check CHECK (scope IN ('org','agent'));
ALTER TABLE memory_docs DROP COLUMN IF EXISTS office_id;
