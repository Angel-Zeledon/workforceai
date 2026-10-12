-- Bring-your-own-model (G3): an organization's own model provider keys and its active provider.
--
-- Secrets: sealed_key holds ONLY ciphertext (AES-256-GCM under the organization's data key, wrapped by the
-- KEK that lives outside the database: the same envelope encryption as the connections vault, see
-- internal/vault Seal/Unseal). The clear key is never stored, never returned by the API (only key_last4),
-- and is sent to the agent runtime only on the internal, RUNTIME_TOKEN-authenticated hop.
-- A credential is DELETED (not kept as history) when the owner removes it: the ciphertext goes away.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS org_llm_credentials (
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider       TEXT NOT NULL CHECK (provider IN ('deepseek', 'anthropic', 'openai', 'kimi', 'custom')),
    model          TEXT NOT NULL,
    base_url       TEXT NOT NULL DEFAULT '',
    org_ref        TEXT NOT NULL DEFAULT '',       -- OpenAI organization id (not secret)
    project_ref    TEXT NOT NULL DEFAULT '',       -- OpenAI project id (not secret)
    sealed_key     BYTEA NOT NULL,                 -- ciphertext only
    key_last4      TEXT NOT NULL DEFAULT '',
    updated_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_test_code TEXT NOT NULL DEFAULT '',
    last_test_at   TIMESTAMPTZ,
    PRIMARY KEY (org_id, provider)
);

-- The organization's chosen provider ('' = the platform default).
CREATE TABLE IF NOT EXISTS org_llm_settings (
    org_id          TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    active_provider TEXT NOT NULL DEFAULT '',
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['org_llm_credentials', 'org_llm_settings']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
