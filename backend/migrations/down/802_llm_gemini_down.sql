-- Rollback of 802_llm_gemini.sql. The organizations' Gemini keys are DELETED (the old CHECK cannot hold them) and an
-- organization whose active provider was Gemini goes back to the platform default ('').
DELETE FROM org_llm_credentials WHERE provider = 'gemini';
UPDATE org_llm_settings SET active_provider = '' WHERE active_provider = 'gemini';
ALTER TABLE org_llm_credentials DROP CONSTRAINT IF EXISTS org_llm_credentials_provider_check;
ALTER TABLE org_llm_credentials ADD CONSTRAINT org_llm_credentials_provider_check
    CHECK (provider IN ('deepseek', 'anthropic', 'openai', 'kimi', 'custom'));
