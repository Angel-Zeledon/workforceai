-- W5: approvals are looked up by task (project monitor, approvals of one request).
-- Additive, idempotent. RLS is unchanged (approvals is already covered by 202).
CREATE INDEX IF NOT EXISTS approvals_org_task_idx ON approvals (org_id, task_id);
