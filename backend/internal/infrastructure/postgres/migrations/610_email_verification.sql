-- Email verification: when EMAIL_VERIFICATION is on, a new account must prove it owns its address (a one-time link)
-- before it gets a session. Tokens are stored hashed, single-use and expiring.
--
-- users and email_verifications are NOT under RLS (same as the other auth tables: read before a tenant is known).
-- Idempotent.

ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

-- Every account that existed before this migration keeps working.
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

CREATE TABLE IF NOT EXISTS email_verifications (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS email_verifications_user_idx ON email_verifications (user_id) WHERE used_at IS NULL;
