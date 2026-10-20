import { call } from "./api";
import type { GoldenCheck, QualityLevel, QualityTrend } from "./qualityLogic";

/** Contract: backend/internal/api/quality.go and docs/architecture/quality-score.md (additive, /api/v1). */

export type ComponentKey = "approvals" | "tasks" | "review" | "retries" | "golden";

export interface QualityComponent {
  key: ComponentKey; weight: number; effective_weight: number;
  /** 0..1; null when the sample is under its minimum */
  rate: number | null; sample: number; min_sample: number; included: boolean;
}
export interface QualityCounts {
  approved: number; rejected: number; tasks_done: number; tasks_failed: number; reviewed: number; review_first_pass: number;
  retries: number; retries_tracked: boolean; golden_runs: number; golden_passed: number;
}
export interface AgentQuality {
  agent_id: string; name: string; role: string;
  status: "ok" | "insufficient_data";
  /** 0..100; null when status is insufficient_data */
  score: number | null; level?: QualityLevel;
  components: QualityComponent[]; sample: number;
  trend: QualityTrend; trend_delta: number | null; previous_score: number | null;
  counts: QualityCounts; golden_cases: number;
}
export interface Methodology {
  version: number; window_days: number; min_total_sample: number; trend_delta: number; advisory: boolean;
  components: { key: ComponentKey; weight: number; min_sample: number }[];
}
export interface QualityOverviewResponse { methodology: Methodology; generated_at: string; agents: AgentQuality[] }
export interface QualityAgentResponse { methodology: Methodology; generated_at: string; agent: AgentQuality }

export interface GoldenCase {
  id: string; agent_id: string; name: string; input: string; checks: GoldenCheck[]; active: boolean;
  created_by: string; created_at: string; updated_at: string;
}
export interface GoldenCaseInput { name: string; input: string; checks: GoldenCheck[]; active?: boolean }
export interface GoldenRun {
  id: string; case_id: string; agent_id: string; passed: boolean; checks_passed: number; checks_total: number;
  task_id?: string; note?: string; recorded_by: string; created_at: string;
}

const win = (days?: number) => (days ? `?window_days=${days}` : "");

export const qualityApi = {
  overview: (days?: number) => call<QualityOverviewResponse>("GET", `/quality/agents${win(days)}`),
  agent: (id: string, days?: number) => call<QualityAgentResponse>("GET", `/agents/${encodeURIComponent(id)}/quality${win(days)}`),
  cases: (agentId: string) => call<GoldenCase[]>("GET", `/agents/${encodeURIComponent(agentId)}/golden-cases`),
  createCase: (agentId: string, body: GoldenCaseInput) => call<GoldenCase>("POST", `/agents/${encodeURIComponent(agentId)}/golden-cases`, body),
  updateCase: (id: string, body: GoldenCaseInput) => call<GoldenCase>("PUT", `/golden-cases/${encodeURIComponent(id)}`, body),
  deleteCase: (id: string) => call<unknown>("DELETE", `/golden-cases/${encodeURIComponent(id)}`),
  runs: (id: string, limit = 5) => call<GoldenRun[]>("GET", `/golden-cases/${encodeURIComponent(id)}/runs?limit=${limit}`),
  /** The output is evaluated by the server against the case checks and is not stored. */
  recordRun: (id: string, output: string) => call<GoldenRun>("POST", `/golden-cases/${encodeURIComponent(id)}/runs`, { output }),
};
