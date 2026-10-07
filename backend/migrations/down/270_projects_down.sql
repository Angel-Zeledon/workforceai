-- Rollback of 270_projects.sql. Drops every project and saved project template.
DROP TABLE IF EXISTS project_templates;
DROP TABLE IF EXISTS projects;
