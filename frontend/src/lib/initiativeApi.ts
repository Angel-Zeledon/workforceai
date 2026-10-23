import { call } from "./api";

/** Proactive suggestions (B2). Contract: backend/internal/api/initiative.go, docs/architecture/initiative.md. */
export type InitiativeStatus = "pending" | "accepted" | "dismissed";

export interface Initiative {
  id: string; agent_id: string; signal: string; title: string; body: string;
  status: InitiativeStatus; created_at: string;
  signal_key?: string;
  /** The request a person submits by accepting it. */
  prompt?: string;
  /** Values behind the text (metric signals, migration 800): the UI renders the suggestion from signal + params. */
  params?: Record<string, string>;
  resolved_at?: string; resolved_by?: string;
  /** Set once a person accepted it: the normal request it became. */
  request_id?: string;
}

/** Per-signal switches and thresholds (ranges in initiativeLogic.ts SIGNAL_FIELDS). */
export interface SignalSettings {
  metric_change: { enabled: boolean; change_pct: number; margin_points: number };
  spend_pace: { enabled: boolean; pace_pct: number; min_days: number };
  approval_aging: { enabled: boolean; min_age_hours: number; min_count: number };
  quality_drop: { enabled: boolean; drop_points: number };
}

export interface InitiativeSettings {
  enabled: boolean; daily_limit: number; monthly_limit: number;
  /** Per-agent daily override (0 mutes the agent). */
  agent_daily?: Record<string, number>;
  /** Metric signals; always present on read from a server with migration 800. */
  signals?: SignalSettings;
}

export interface ScanResult { signal: string; signal_key: string; agent_id?: string; raised: boolean; reason?: string; initiative_id?: string }

export const initiativeApi = {
  list: (status: InitiativeStatus | "" = "pending") => call<Initiative[]>("GET", `/initiatives${status ? `?status=${status}` : ""}`),
  accept: (id: string) => call<Initiative>("POST", `/initiatives/${encodeURIComponent(id)}/accept`),
  dismiss: (id: string) => call<Initiative>("POST", `/initiatives/${encodeURIComponent(id)}/dismiss`),
  settings: () => call<InitiativeSettings>("GET", "/settings/initiative"),
  putSettings: (s: InitiativeSettings) => call<InitiativeSettings>("PUT", "/settings/initiative", s),
  /** Evaluates the metric signals now (owner/admin). */
  scan: () => call<{ results: ScanResult[]; raised: number }>("POST", "/initiatives/scan"),
};
