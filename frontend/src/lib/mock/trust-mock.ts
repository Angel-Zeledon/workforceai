import type { Agent } from "../types";
import type { AgentTrust, TrustAgentResponse, TrustMethodology } from "../trustApi";

/** Mirrors backend/internal/trust/assess.go (thresholds) and roles.AutonomyCeiling for the seed roles. Update both together. */
const METHODOLOGY: TrustMethodology = {
  version: 1, window_days: 30, probation_days: 14, probation_decisions: 10, min_task_sample: 5, burst_hours: 72,
  decline_cooldown_days: 7, revert_days: 7, promotion_needs_human: true, auto_demotion: true,
  promotion: [
    { key: "decisions", op: ">=", threshold: 20 }, { key: "approval_rate", op: ">=", threshold: 0.95 },
    { key: "high_risk_rejections", op: "<=", threshold: 0 }, { key: "policy_denials", op: "<=", threshold: 0 },
    { key: "task_failure_rate", op: "<=", threshold: 0.1 }, { key: "quality", op: ">=", threshold: 75 },
  ],
  demotion: [
    { key: "high_risk_rejection", op: ">=", threshold: 1, auto: true }, { key: "rejection_burst", op: ">=", threshold: 3, auto: true },
    { key: "policy_incident", op: ">=", threshold: 3, auto: true }, { key: "low_approval_rate", op: "<", threshold: 0.8 },
    { key: "high_task_failure_rate", op: ">", threshold: 0.3 }, { key: "low_quality", op: "<", threshold: 50 },
  ],
};

/** Ceiling per seed role (template ceiling capped by its risk tier); unknown roles get the most restrictive one. */
const CEILING: Record<string, Agent["autonomy"]> = {
  assistant: "autonomous", analyst: "autonomous", data_analyst: "autonomous", education: "autonomous", internal_auditor: "autonomous", project_manager: "autonomous",
  sales: "rules", accounting: "rules", hr: "rules", legal: "rules", operations: "rules", finance_treasury: "rules", software_engineer: "rules",
};
const UP: Record<string, Agent["autonomy"] | undefined> = { suggest: "approve_each", approve_each: "rules", rules: "autonomous" };
const RANK: Record<string, number> = { suggest: 0, approve_each: 1, rules: 2, autonomous: 3 };

/** The demo keeps no decision history: every agent honestly starts its probation now, with nothing to suggest. */
export function mockTrust(a: Agent, now = new Date()): TrustAgentResponse {
  const ceiling = CEILING[a.role] ?? "approve_each";
  const up = UP[a.autonomy];
  const next = up && RANK[up] <= RANK[ceiling] ? up : undefined;
  const iso = now.toISOString();
  const zero = { approved: 0, rejected: 0, high_risk_rejected: 0, recent_rejected: 0, policy_denials: 0, tasks_done: 0, tasks_failed: 0, tasks_blocked: 0 };
  const agent: AgentTrust = {
    agent_id: a.id, name: a.name, role: a.role, level: a.autonomy, ceiling, tracking_since: iso, level_since: iso, evidence_since: iso,
    counts: zero, quality: null, recommendation: "probation", next_level: next, at_ceiling: !next,
    probation: { active: true, until: new Date(now.getTime() + 14 * 86_400_000).toISOString(), decisions: 0, min_decisions: 10, days: 14 },
    criteria: METHODOLOGY.promotion.map((c) => ({ key: c.key, op: c.op, threshold: c.threshold,
      value: c.key === "decisions" || c.key === "high_risk_rejections" || c.key === "policy_denials" ? 0 : null,
      status: c.key === "decisions" ? "unmet" as const : c.key === "high_risk_rejections" || c.key === "policy_denials" ? "met" as const : "not_available" as const })),
    triggers: METHODOLOGY.demotion.map((d) => ({ key: d.key, op: d.op, threshold: d.threshold, auto: !!d.auto, fired: false, value: null })),
    progress: 0.667, pending: null, last_change: null, can_revert: false,
  };
  return { methodology: METHODOLOGY, generated_at: iso, agent };
}
