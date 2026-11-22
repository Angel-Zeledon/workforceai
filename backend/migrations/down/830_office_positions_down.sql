-- Down of 830_office_positions.sql: positions and who holds them are lost. Agents keep their direct skills, their
-- trade's skills and their folder rules; without a position every agent sees its department as before.
DROP TABLE IF EXISTS agent_positions;
DROP TABLE IF EXISTS office_positions;
