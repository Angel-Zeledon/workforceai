-- Rollback of 310_model_policy.sql. Organizations go back to "no restriction"
-- (the server ceiling ALLOWED_PROVIDERS still applies).
DROP TABLE IF EXISTS org_model_policy;
