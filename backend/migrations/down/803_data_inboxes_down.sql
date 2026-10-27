-- Rollback of 803_data_inboxes.sql. Every data inbox stops accepting pushes at once (the tokens cannot be
-- recovered) and the push history (metadata) is lost. The files the inboxes already wrote stay in the drive,
-- attributed to their inbox principal.
DROP TABLE IF EXISTS data_inbox_pushes;
DROP TABLE IF EXISTS data_inboxes;
