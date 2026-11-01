-- Plan catalog: free, pro and max (docs/architecture/approvals-notify.md section 3.1, internal/policy/plans.go).
--
-- org_plans.plan now also accepts 'pro' and 'max'. Both carry their AI allowance in cap_usd and, like 'free', it is a
-- HARD monthly cap. 'paid' stays for the legacy "no plan" of a deployment without plans (no cap, no plan limits).
--
-- Idempotent.
ALTER TABLE org_plans DROP CONSTRAINT IF EXISTS org_plans_plan_check;
ALTER TABLE org_plans ADD CONSTRAINT org_plans_plan_check CHECK (plan IN ('free','pro','max','paid'));
