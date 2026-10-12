-- Rollback of 510_skills.sql (the skill library metadata is dropped; the files
-- stay in the drive under /Shared/Skills).
DROP TABLE IF EXISTS skill_assignments;
DROP TABLE IF EXISTS skill_versions;
DROP TABLE IF EXISTS skills;
DROP FUNCTION IF EXISTS skill_versions_append_only();
