-- Rollback of 789_quality_golden.sql. The golden cases and their recorded runs are lost; the quality score
-- itself is computed from existing tables and is not affected.
DROP TABLE IF EXISTS quality_golden_runs;
DROP TABLE IF EXISTS quality_golden_cases;
