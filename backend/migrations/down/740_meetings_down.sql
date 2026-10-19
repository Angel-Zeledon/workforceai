-- Rollback of 740_meetings.sql. Meetings and their minutes rows are dropped; minutes files saved in the drive are untouched.
DROP TABLE IF EXISTS meeting_minutes;
DROP TABLE IF EXISTS meetings;
