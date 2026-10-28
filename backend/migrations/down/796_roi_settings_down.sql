-- Rollback of 796_roi_settings.sql. The organizations' hours-saved model (hourly rate, minutes per task) is lost;
-- GET /roi then falls back to the built-in assumption and shows hours only. No other table depends on it.
DROP TABLE IF EXISTS roi_settings;
