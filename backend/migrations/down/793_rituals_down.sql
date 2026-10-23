-- Rollback of 793_rituals.sql. The stand-up and retro history, their proposals and the ritual settings are lost.
-- Instruction notes a person already accepted stay in the agent work profile (they are ordinary profile versions).
DROP TABLE IF EXISTS ritual_proposals;
DROP TABLE IF EXISTS rituals;
DROP TABLE IF EXISTS ritual_settings;
