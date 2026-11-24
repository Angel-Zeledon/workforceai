-- Down of 831_helper_groups.sql: the groups and the helper mark of the tasks are lost. The tasks of the parts stay as
-- ordinary tasks of the responsible employee (their results and cost are kept).
DROP TABLE IF EXISTS helper_groups;
ALTER TABLE tasks DROP COLUMN IF EXISTS helper;
