import type { Agent } from "../types";
import type { AgentQuality, ComponentKey, Methodology, QualityOverviewResponse } from "../qualityApi";

/** Mirrors backend/internal/quality/score.go (weights and minimum samples). Update both together. */
const SPECS: { key: ComponentKey; weight: number; min_sample: number }[] = [
  { key: "approvals", weight: 0.3, min_sample: 5 }, { key: "tasks", weight: 0.25, min_sample: 5 }, { key: "review", weight: 0.15, min_sample: 5 },
  { key: "retries", weight: 0.1, min_sample: 5 }, { key: "golden", weight: 0.2, min_sample: 3 },
];

/** The demo keeps no approval or task history to score, so every agent honestly reports "insufficient data". */
export function mockQuality(agents: Agent[]): QualityOverviewResponse {
  const methodology: Methodology = { version: 1, window_days: 30, min_total_sample: 10, trend_delta: 5, advisory: true, components: SPECS };
  return {
    methodology, generated_at: new Date().toISOString(),
    agents: agents.map((a): AgentQuality => ({
      agent_id: a.id, name: a.name, role: a.role, status: "insufficient_data", score: null, sample: 0,
      components: SPECS.map((s) => ({ key: s.key, weight: s.weight, effective_weight: 0, rate: null, sample: 0, min_sample: s.min_sample, included: false })),
      trend: "unknown", trend_delta: null, previous_score: null, golden_cases: 0,
      counts: { approved: 0, rejected: 0, tasks_done: 0, tasks_failed: 0, reviewed: 0, review_first_pass: 0, retries: 0, retries_tracked: false, golden_runs: 0, golden_passed: 0 },
    })),
  };
}
