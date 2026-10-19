-- Rollback of 711_api_access.sql (API tokens stop working; webhook endpoints and the outbox are dropped).
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
DROP POLICY IF EXISTS token_lookup ON api_tokens;
DROP TABLE IF EXISTS api_tokens;
