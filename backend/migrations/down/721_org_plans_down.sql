-- Rollback of 721_org_plans.sql. Explicit plans are dropped: organizations fall back to the deployment default
-- (FREE_PLAN_CAP_USD). Spend itself lives in the cost ledger and is untouched.
DROP TABLE IF EXISTS org_plans;
