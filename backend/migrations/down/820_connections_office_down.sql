-- Down of 820_connections_office.sql: every connection goes back to General (the whole company); the grants of agents
-- of other offices that the department was holding back serve again. Review them before running it.
ALTER TABLE connections DROP COLUMN IF EXISTS office_id;
