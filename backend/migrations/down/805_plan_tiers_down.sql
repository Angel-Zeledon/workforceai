-- Reverts 805_plan_tiers.sql. Pro and Max rows go back to the legacy paid plan (no cap) before the old check returns.
UPDATE org_plans SET plan = 'paid' WHERE plan IN ('pro','max');
ALTER TABLE org_plans DROP CONSTRAINT IF EXISTS org_plans_plan_check;
ALTER TABLE org_plans ADD CONSTRAINT org_plans_plan_check CHECK (plan IN ('free','paid'));
