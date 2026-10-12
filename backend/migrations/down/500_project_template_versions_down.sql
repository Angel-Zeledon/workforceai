-- Rollback of 500_project_template_versions.sql. Drops the history of the
-- organization templates (the current templates in project_templates stay).
DROP TABLE IF EXISTS project_template_versions;
