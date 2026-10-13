-- Two-factor authentication (TOTP) and password-reset links. Idempotent. Neither table is under RLS (auth tables, read
-- before a tenant is known).

-- One table already holds hashed single-use e-mail tokens: it now says what each token is for ('verify' or 'reset').
ALTER TABLE email_verifications ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'verify';
DROP INDEX IF EXISTS email_verifications_user_idx;
CREATE INDEX IF NOT EXISTS email_verifications_user_purpose_idx ON email_verifications (user_id, purpose) WHERE used_at IS NULL;

-- The TOTP secret is stored encrypted (AES-256-GCM, bound to the user id); recovery codes only as SHA-256 hashes.
CREATE TABLE IF NOT EXISTS user_mfa (
    user_id         TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc      TEXT NOT NULL,
    enabled_at      TIMESTAMPTZ,
    last_step       BIGINT NOT NULL DEFAULT 0,
    recovery_hashes TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
