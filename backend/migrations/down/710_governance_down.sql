-- Rollback of 710_governance.sql (department tags, department limits and retention policies are dropped).
DROP TABLE IF EXISTS org_retention;
DROP TABLE IF EXISTS department_limits;
DROP INDEX IF EXISTS agents_department_idx;
ALTER TABLE agents DROP COLUMN IF EXISTS department;
