-- Rollback of 440_plan_changes.sql. Applied changes stay in the plan (they are
-- ordinary nodes and tasks); only the proposal history is dropped.
DROP TABLE IF EXISTS plan_changes;
