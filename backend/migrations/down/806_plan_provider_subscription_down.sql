-- Reverts 806_plan_provider_subscription.sql.
ALTER TABLE org_plans DROP COLUMN IF EXISTS provider_pending;
ALTER TABLE org_plans DROP COLUMN IF EXISTS provider_sub;
