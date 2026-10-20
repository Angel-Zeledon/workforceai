-- Rollback of 787_initiatives.sql. Suggestions (and their history) and the initiative budget are dropped;
-- agents simply stop raising suggestions.
DROP TABLE IF EXISTS initiative_settings;
DROP TABLE IF EXISTS initiatives;
