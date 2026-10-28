-- Rollback of 792_away_marks.sql. The last-seen marks are dropped; the next visit of each person simply starts
-- without a "while you were away" summary. No other data depends on this table.
DROP TABLE IF EXISTS away_marks;
