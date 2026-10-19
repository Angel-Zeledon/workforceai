-- Rollback of 700_identity_sso.sql. SSO setup and sign-in policy are configuration; accounts are untouched.
DROP TABLE IF EXISTS org_identity_policy;
DROP TABLE IF EXISTS org_sso_configs;
