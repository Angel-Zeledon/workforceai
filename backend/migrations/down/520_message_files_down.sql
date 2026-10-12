-- Rollback of 520_message_files.sql (chat messages lose their file references; the
-- files themselves stay in the drive).
ALTER TABLE messages DROP COLUMN IF EXISTS files;
