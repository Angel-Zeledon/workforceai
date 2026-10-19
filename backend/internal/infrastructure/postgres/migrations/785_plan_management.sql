-- Plan management (docs/architecture/approvals-notify.md section 3).
--
-- Until now the cap of a free plan counted the spend of the whole life of the organization. It now resets every calendar
-- month (UTC): spend_base_usd is the lifetime spend recorded at period_start, and the plan only counts
-- lifetime - spend_base_usd. A job (notify.PlanResetter) moves period_start to the 1st of the month atomically, so replicas
-- never reset twice. provider / provider_ref are placeholders for a payment provider (docs: "Billing provider"); nothing
-- charges anyone today.
--
-- Idempotent.
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS period_start   TIMESTAMPTZ;
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS spend_base_usd DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (spend_base_usd >= 0);
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS provider       TEXT NOT NULL DEFAULT '';
ALTER TABLE org_plans ADD COLUMN IF NOT EXISTS provider_ref   TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS org_plans_period_idx ON org_plans (period_start);
