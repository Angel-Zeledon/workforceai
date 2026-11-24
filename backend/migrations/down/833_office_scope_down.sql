-- Down of 833_office_scope.sql: the office a message, request or task was asked from is forgotten (they read as General).
DROP INDEX IF EXISTS messages_office_idx;
DROP INDEX IF EXISTS requests_office_idx;
ALTER TABLE messages DROP COLUMN IF EXISTS office_id;
ALTER TABLE requests DROP COLUMN IF EXISTS office_id;
ALTER TABLE tasks    DROP COLUMN IF EXISTS office_id;
