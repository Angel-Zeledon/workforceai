-- Rollback of 800_initiative_signals.sql. The per-signal settings and the parameters of stored suggestions are
-- dropped; suggestions keep their stored title and body, and the code before 800 never reads these columns.
ALTER TABLE initiatives DROP COLUMN IF EXISTS params;
ALTER TABLE initiative_settings DROP COLUMN IF EXISTS signals;
