-- Trial accounts ("try it without signing up"): a throwaway account that stops working at trial_expires_at.
-- NULL = a normal account. Idempotent. users is not under RLS (read before a tenant is known).

ALTER TABLE users ADD COLUMN IF NOT EXISTS trial_expires_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS users_trial_expiry_idx ON users (trial_expires_at) WHERE trial_expires_at IS NOT NULL;
