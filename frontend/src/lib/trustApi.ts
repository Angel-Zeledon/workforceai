import { call } from "./api";
import type { Agent } from "./types";

/** Contract: backend/internal/api/trust.go and docs/architecture/trust-ladder.md (additive, /api/v1). */

export type Level = Agent["autonomy"];
export type Recommendation = "none" | "promote" | "demote" | "demote_auto" | "probation" | "at_ceiling" | "cooldown" | "gathering";
export type CriterionKey = "decisions" | "approval_rate" | "high_risk_rejections" | "policy_denials" | "task_failure_rate" | "quality";
export type TriggerKey = "high_risk_rejection" | "rejection_burst" | "policy_incident" | "low_approval_rate" | "high_task_failure_rate" | "low_quality";
export type ProposalStatus = "pending" | "accepted" | "declined" | "applied" | "reverted" | "superseded";

export interface TrustCounts {
  approved: number; rejected: number; high_risk_rejected: number; recent_rejected: number; policy_denials: number;
  tasks_done: number; tasks_failed: number; tasks_blocked: number;
}
export interface Criterion { key: CriterionKey; value: number | null; threshold: number; op: string; status: "met" | "unmet" | "not_available" }
export interface Trigger { key: TriggerKey; value: number | null; threshold: number; op: string; auto: boolean; fired: boolean }
export interface Probation { active: boolean; until: string; decisions: number; min_decisions: number; days: number }
export interface Proposal {
  id: string; agent_id: string; kind: "promotion" | "demotion"; from_level: Level; to_level: Level; status: ProposalStatus; auto: boolean;
  reasons: string[]; created_at: string; resolved_at?: string; resolved_by?: string; note?: string;
  evidence: { since: string; counts: TrustCounts; criteria: Criterion[]; triggers: Trigger[]; ceiling: Level; methodology_version: number };
}
export interface AgentTrust {
  agent_id: string; name: string; role: string; level: Level; ceiling: Level;
  tracking_since: string; level_since: string; evidence_since: string;
  counts: TrustCounts; quality: { score: number; trend: string } | null;
  recommendation: Recommendation; target?: Level; next_level?: Level; at_ceiling: boolean;
  probation: Probation; criteria: Criterion[]; triggers: Trigger[];
  /** 0..1 toward the next level */
  progress: number;
  pending: Proposal | null; last_change: Proposal | null; can_revert: boolean;
}
export interface TrustMethodology {
  version: number; window_days: number; probation_days: number; probation_decisions: number; min_task_sample: number;
  burst_hours: number; decline_cooldown_days: number; revert_days: number;
  promotion: { key: CriterionKey; op: string; threshold: number }[];
  demotion: { key: TriggerKey; op: string; threshold: number; auto?: boolean }[];
  promotion_needs_human: boolean; auto_demotion: boolean;
}
export interface TrustAgentResponse { methodology: TrustMethodology; generated_at: string; agent: AgentTrust }

const enc = encodeURIComponent;

export const trustApi = {
  agent: (id: string) => call<TrustAgentResponse>("GET", `/agents/${enc(id)}/trust`),
  overview: () => call<{ methodology: TrustMethodology; generated_at: string; agents: AgentTrust[] }>("GET", "/trust/agents"),
  proposals: (id: string, limit = 10) => call<Proposal[]>("GET", `/agents/${enc(id)}/trust/proposals?limit=${limit}`),
  evaluate: (id: string) => call<{ methodology: TrustMethodology; agent: AgentTrust }>("POST", `/agents/${enc(id)}/trust/evaluate`),
  accept: (pid: string, note = "") => call<Proposal>("POST", `/trust/proposals/${enc(pid)}/accept`, { note }),
  decline: (pid: string, note = "") => call<Proposal>("POST", `/trust/proposals/${enc(pid)}/decline`, { note }),
  revert: (pid: string, note = "") => call<Proposal>("POST", `/trust/proposals/${enc(pid)}/revert`, { note }),
};
