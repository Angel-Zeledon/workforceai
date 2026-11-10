-- Down of 819_skills_office.sql: skills of a department become skills of the whole company (every agent can then be
-- given them and everybody sees them). Move or delete the private departments' skills first if that matters.
ALTER TABLE skills DROP COLUMN IF EXISTS office_id;
