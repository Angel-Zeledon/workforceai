-- Rollback of 580_llm_credentials.sql. The organizations' own model keys (ciphertext) are DROPPED: after
-- this every organization uses the platform default model again; nothing else depends on these tables.
DROP TABLE IF EXISTS org_llm_settings;
DROP TABLE IF EXISTS org_llm_credentials;
