-- Rollback of 702_sso_pending.sql. Only in-flight sign-ins (minutes of life) are lost.
DROP TABLE IF EXISTS sso_pending;
