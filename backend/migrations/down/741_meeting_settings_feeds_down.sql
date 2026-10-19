-- Rollback of 741_meeting_settings_feeds.sql. Existing ICS subscription URLs stop working.
DROP TABLE IF EXISTS calendar_feeds;
DROP TABLE IF EXISTS meeting_settings;
