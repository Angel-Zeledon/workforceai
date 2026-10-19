-- Durable retry queue for e-mail / Slack / Teams notifications (docs/architecture/approvals-notify.md).
--
-- Same pattern as webhook_deliveries (711): one row per message and destination, leased with FOR UPDATE SKIP LOCKED
-- (next_attempt_at moves forward while a worker holds it), exponential backoff, and status 'dead' after the last attempt
-- (the dead letter, kept for inspection and pruned after 30 days).
-- payload is the message body (an e-mail or a chat card). It can carry signed approval links, so it is sealed with the
-- vault when the deployment has a key (sealed = true). The webhook URL is NOT stored: it is read from notify_settings at
-- send time, so removing a channel stops its queued messages.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS notify_outbox (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    channel         TEXT NOT NULL CHECK (channel IN ('email', 'slack', 'teams')),
    recipient       TEXT NOT NULL DEFAULT '',
    payload         BYTEA NOT NULL,
    sealed          BOOLEAN NOT NULL DEFAULT false,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'dead')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS notify_outbox_due_idx ON notify_outbox (org_id, next_attempt_at) WHERE status = 'pending';

ALTER TABLE notify_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_outbox FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON notify_outbox;
CREATE POLICY tenant_isolation ON notify_outbox
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON notify_outbox TO app_user;
    END IF;
END
$$;
