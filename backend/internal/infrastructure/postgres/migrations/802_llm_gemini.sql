-- Gemini (Google) as a bring-your-own-model provider: org_llm_credentials.provider accepts 'gemini'.
-- The key is sealed exactly like the others (see 580_llm_credentials.sql); nothing else changes.
--
-- Idempotent: the inline CHECK of 580 got Postgres' default name; it is dropped and recreated by that name.

ALTER TABLE org_llm_credentials DROP CONSTRAINT IF EXISTS org_llm_credentials_provider_check;
ALTER TABLE org_llm_credentials ADD CONSTRAINT org_llm_credentials_provider_check
    CHECK (provider IN ('deepseek', 'anthropic', 'openai', 'kimi', 'gemini', 'custom'));
