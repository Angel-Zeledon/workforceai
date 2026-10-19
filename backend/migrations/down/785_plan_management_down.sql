-- Rollback of 785_plan_management.sql. Caps count the lifetime spend again.
DROP INDEX IF EXISTS org_plans_period_idx;
ALTER TABLE org_plans DROP COLUMN IF EXISTS period_start;
ALTER TABLE org_plans DROP COLUMN IF EXISTS spend_base_usd;
ALTER TABLE org_plans DROP COLUMN IF EXISTS provider;
ALTER TABLE org_plans DROP COLUMN IF EXISTS provider_ref;
