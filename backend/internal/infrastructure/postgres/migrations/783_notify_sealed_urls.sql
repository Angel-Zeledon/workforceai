-- Notification webhook URLs sealed with the vault (docs/architecture/approvals-notify.md).
--
-- slack_webhook_enc / teams_webhook_enc: the incoming-webhook URL encrypted under the organization's data key
-- (internal/vault Seal, purpose "notify-webhook-slack|teams"). The plaintext columns (720) are kept only so that rows
-- written before this migration can be read once and re-sealed by the application; after that they hold ''.
--
-- Idempotent.
ALTER TABLE notify_settings ADD COLUMN IF NOT EXISTS slack_webhook_enc BYTEA;
ALTER TABLE notify_settings ADD COLUMN IF NOT EXISTS teams_webhook_enc BYTEA;
