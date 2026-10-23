-- Rollback of 794_rehearsal.sql. The rehearsal switches and the "would have done" log are dropped; nothing else changes
-- (a rehearsal never executed anything, so there is nothing to undo outside these tables).
DROP TABLE IF EXISTS rehearsal_actions;
DROP TABLE IF EXISTS agent_rehearsal;
