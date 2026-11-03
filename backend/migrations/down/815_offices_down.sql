-- Down of 815_offices.sql: back to one office per organization, every agent visible to everybody. The office
-- memory notes and the per-office 3D layouts are lost (General's layout stays in office_layout).
DROP TABLE IF EXISTS office_layouts;
DROP TABLE IF EXISTS team_notes;
DROP TABLE IF EXISTS agent_access;
DROP TABLE IF EXISTS office_members;
DROP TABLE IF EXISTS offices;
