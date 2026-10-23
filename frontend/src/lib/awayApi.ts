import { call } from "./api";

/** "While you were away" (B3). Contract: backend/internal/api/away.go and docs/architecture/away-summary.md (additive, /api/v1). */

export type AwaySeverity = "high" | "medium" | "low" | "info";
export type AwayKind = "approval" | "alert" | "task_failed" | "task_blocked" | "request_failed" | "request_completed" | "initiative" | "decision";
export type AwayRef = "approval" | "task" | "request" | "report" | "initiative" | "connection" | "agent" | "org";
export type AwaySectionKey = "alerts" | "problems" | "done" | "suggestions" | "decided";

export interface AwayItem {
  kind: AwayKind; ref: AwayRef; ref_id: string;
  /** Data (task, request or approval title), never UI text. */
  title: string; detail?: string;
  /** Machine code for the i18n text (alert kind, decision, reason). */
  code?: string;
  agent_id?: string; severity: AwaySeverity; at: string;
  /** A pending approval created during the absence. */
  new?: boolean;
  /** Identical events folded into this item. */
  repeats?: number;
  risk?: string; required_approvals?: number;
}
export interface AwaySection { key: AwaySectionKey; total: number; items: AwayItem[] }
export interface AwayCounts {
  requests_completed: number; requests_failed: number; tasks_completed: number; tasks_failed: number; tasks_blocked: number;
  approvals_waiting: number; approvals_waiting_new: number; approvals_waiting_other: number; approvals_decided: number;
  initiatives: number; alerts: number; messages: number;
}
export interface AwayFrame { at: string; agent_id: string; state: string; activity?: string }
export interface AwaySummary {
  version: number; method: "deterministic"; generated_at: string;
  first_visit: boolean; since: string | null; until: string | null; away_minutes: number; min_hours: number;
  clipped: boolean; dismissed: boolean; quiet: boolean;
  /** The frontend opens the summary on its own only when this is true. */
  show: boolean;
  counts: AwayCounts;
  spend: { usd: number; calls: number; top: { agent_id: string; usd: number; calls: number }[] };
  act_now: AwayItem[];
  sections: AwaySection[];
  agents: { agent_id: string; name: string; tasks_completed: number; tasks_failed: number; usd: number; state: string }[];
  timelapse: { available: boolean; total: number; sampled: boolean; frames: AwayFrame[] };
}
export interface AwayMark { last_seen_at: string; visit_started_at: string; away_since?: string; dismissed_at?: string }

export const awayApi = {
  summary: (minHours?: number) => call<AwaySummary>("GET", `/away-summary${minHours === undefined ? "" : `?min_hours=${minHours}`}`),
  heartbeat: () => call<AwayMark>("POST", "/away-summary/heartbeat"),
  dismiss: () => call<AwayMark>("POST", "/away-summary/dismiss"),
};
