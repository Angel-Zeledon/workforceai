-- Verified-domain re-check (docs/architecture/identity.md section 3). Idempotent.
--
-- lapsed_since: set by the janitor the first time the DNS challenge record is found missing for a verified domain; cleared
-- when the record is back. When it has been set for longer than the grace period the verification is revoked. NULL = the
-- record was present at the last check.
ALTER TABLE org_domains ADD COLUMN IF NOT EXISTS lapsed_since TIMESTAMPTZ;
