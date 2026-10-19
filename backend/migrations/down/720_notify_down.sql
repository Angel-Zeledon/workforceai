-- Rollback of 720_notify.sql. Channel settings and the replay ledger are dropped; approvals keep working in the app
-- (links already sent stop working: with no ledger they are refused).
DROP TABLE IF EXISTS notify_token_use;
DROP TABLE IF EXISTS notify_settings;
