-- Generic HTTP sources: a custom_api connection carries the configuration agents need to query any platform with an
-- HTTP API (base URL, how the API key travels, optional named read endpoints and what lives there). It is admin-written
-- organization configuration and holds NO secret: the API key stays in the vault (credentials table) like every other
-- credential. NULL for every other provider. Spec: docs/architecture/integrations-credentials.md, section 18.8.
--
-- Forward-only and idempotent. The connections table already has org RLS (240_connections.sql).

ALTER TABLE connections ADD COLUMN IF NOT EXISTS source JSONB;
