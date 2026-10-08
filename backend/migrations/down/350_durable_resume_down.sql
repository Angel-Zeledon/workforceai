-- Rollback of 350_durable_resume.sql. Requests waiting on a plan review or a
-- cost confirmation, Tool Gateway approvals and chat echoes stop being
-- resumable; the at-most-once record of executed approvals is dropped.
DROP TABLE IF EXISTS approval_executions;
ALTER TABLE task_checkpoints DROP COLUMN IF EXISTS gateway;
ALTER TABLE request_runs     DROP COLUMN IF EXISTS chat;
ALTER TABLE request_runs     DROP COLUMN IF EXISTS gate;
