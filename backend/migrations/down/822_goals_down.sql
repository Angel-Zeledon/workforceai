-- Down of 822_goals.sql: the work forgets which goal it contributed to and the goals are lost.
ALTER TABLE requests DROP CONSTRAINT IF EXISTS requests_goal_fk;
DROP INDEX IF EXISTS requests_goal_idx;
ALTER TABLE requests DROP COLUMN IF EXISTS goal_id;
DROP TABLE IF EXISTS goals;
