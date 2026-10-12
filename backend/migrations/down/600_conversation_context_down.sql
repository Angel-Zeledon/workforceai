-- Rollback of 600_conversation_context.sql. Messages are untouched; the model sees the
-- whole recent history again (the epochs and summaries are dropped).
DROP TABLE IF EXISTS conversation_contexts;
