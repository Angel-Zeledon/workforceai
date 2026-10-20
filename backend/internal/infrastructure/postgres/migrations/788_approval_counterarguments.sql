-- Devil's advocate (B8): the advisory review of a HIGH-RISK approval, generated once on demand and cached here so it
-- is neither regenerated nor charged twice. One row per approval. It holds no secrets: it is derived from the
-- approval card, which is already sanitized. It never changes an approval; it is a second opinion for the human.
-- Spec: docs/architecture/devils-advocate.md.
--
-- Forward-only and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS approval_counterarguments (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    approval_id     TEXT NOT NULL,
    counterargument TEXT NOT NULL,
    risks           JSONB NOT NULL DEFAULT '[]',
    checks          JSONB NOT NULL DEFAULT '[]',
    locale          TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    provider        TEXT NOT NULL DEFAULT '',
    cost_usd        DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    generated_by    TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, approval_id)
);

DO $$
BEGIN
    ALTER TABLE approval_counterarguments ENABLE ROW LEVEL SECURITY;
    ALTER TABLE approval_counterarguments FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON approval_counterarguments;
    CREATE POLICY tenant_isolation ON approval_counterarguments USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        -- A review is written once and never edited by the application (Reset deletes it with the approval data).
        GRANT SELECT, INSERT, DELETE ON approval_counterarguments TO app_user;
    END IF;
END
$$;
