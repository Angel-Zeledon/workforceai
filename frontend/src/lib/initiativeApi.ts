import { call } from "./api";

/** Proactive suggestions (B2). Contract: backend/internal/api/initiative.go, docs/architecture/initiative.md. */
export type InitiativeStatus = "pending" | "accepted" | "dismissed";

export interface Initiative {
  id: string; agent_id: string; signal: string; title: string; body: string;
  status: InitiativeStatus; created_at: string;
  resolved_at?: string; resolved_by?: string;
  /** Set once a person accepted it: the normal request it became. */
  request_id?: string;
}

export interface InitiativeSettings {
  enabled: boolean; daily_limit: number; monthly_limit: number;
  /** Per-agent daily override (0 mutes the agent). */
  agent_daily?: Record<string, number>;
}

export const initiativeApi = {
  list: (status: InitiativeStatus | "" = "pending") => call<Initiative[]>("GET", `/initiatives${status ? `?status=${status}` : ""}`),
  accept: (id: string) => call<Initiative>("POST", `/initiatives/${encodeURIComponent(id)}/accept`),
  dismiss: (id: string) => call<Initiative>("POST", `/initiatives/${encodeURIComponent(id)}/dismiss`),
  settings: () => call<InitiativeSettings>("GET", "/settings/initiative"),
  putSettings: (s: InitiativeSettings) => call<InitiativeSettings>("PUT", "/settings/initiative", s),
};
