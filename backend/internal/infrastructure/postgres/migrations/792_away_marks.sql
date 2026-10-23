-- "While you were away" (B3, docs/architecture/away-summary.md): when each person was last seen in each organization.
--
-- One row per (organization, user). The frontend sends a heartbeat while the tab is visible; a heartbeat after a gap
-- (30 minutes by default) starts a new visit: away_since keeps the end of the previous visit and visit_started_at the
-- start of the new one, so the summary covers exactly the absence. dismissed_at is when the person closed the
-- summary of the current visit. Personal presence metadata only: no content, no secrets. The summary itself is
-- computed on read from events, approvals and usage_entries; nothing else is stored.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS away_marks (
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          TEXT NOT NULL,
    last_seen_at     TIMESTAMPTZ NOT NULL,
    visit_started_at TIMESTAMPTZ NOT NULL,
    away_since       TIMESTAMPTZ,
    dismissed_at     TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

DO $$
BEGIN
    ALTER TABLE away_marks ENABLE ROW LEVEL SECURITY;
    ALTER TABLE away_marks FORCE  ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON away_marks;
    CREATE POLICY tenant_isolation ON away_marks
        USING      (org_id = app_current_org())
        WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON away_marks TO app_user;
    END IF;
END
$$;
