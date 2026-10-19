-- Rollback of 783_notify_sealed_urls.sql. WARNING: sealed URLs are lost (they cannot be decrypted without the application);
-- the Slack and Teams channels must be configured again.
ALTER TABLE notify_settings DROP COLUMN IF EXISTS slack_webhook_enc;
ALTER TABLE notify_settings DROP COLUMN IF EXISTS teams_webhook_enc;
