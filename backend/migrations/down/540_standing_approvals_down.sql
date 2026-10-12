-- Rollback of 540_standing_approvals.sql. Standing approvals (and their history) are dropped;
-- approvals keep working: they just lose the tool/project/eligibility hints.
DROP TABLE IF EXISTS standing_approvals;
ALTER TABLE approvals DROP COLUMN IF EXISTS standing_eligible;
ALTER TABLE approvals DROP COLUMN IF EXISTS project_id;
ALTER TABLE approvals DROP COLUMN IF EXISTS tool;
