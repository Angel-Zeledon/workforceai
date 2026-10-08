-- Rollback of 360_window_counters.sql. Windowed limits and anomaly baselines
-- go back to process memory (a restart resets them).
DROP TABLE IF EXISTS window_counters;
