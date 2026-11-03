-- Plans paid with ONVO Pay (docs/architecture/billing-onvo.md, internal/billing).
--
-- provider / provider_ref (785) are the payment provider and its customer ("onvo", the ONVO customer id).
-- provider_sub is the subscription the plan in force comes from; provider_pending a subscription created for a
-- purchase or a plan change whose first payment was not confirmed yet. When it is paid it becomes provider_sub (the
-- previous one is cancelled first, so nobody pays two plans).
--
-- Idempotent.
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS provider_sub     TEXT NOT NULL DEFAULT '';
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS provider_pending TEXT NOT NULL DEFAULT '';
