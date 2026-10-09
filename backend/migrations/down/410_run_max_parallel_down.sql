-- Rollback of 410_run_max_parallel.sql. Resumed requests fall back to MAX_PARALLEL.
ALTER TABLE request_runs DROP COLUMN IF EXISTS max_parallel;
