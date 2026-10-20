import { call } from "./api";

/** Contract: backend/internal/notify (docs/architecture/approvals-notify.md). */

export interface NotifySettings {
  email_enabled: boolean; email_to: string[]; min_risk: "low" | "medium" | "high"; links_enabled: boolean; locale: "es" | "en" | "pt";
  slack_configured: boolean; slack_masked?: string; teams_configured: boolean; teams_masked?: string;
  /** The deployment can send e-mail / sign approval links. */
  email_available: boolean; links_available: boolean;
}
export interface NotifyInput {
  email_enabled?: boolean; email_to?: string[]; min_risk?: NotifySettings["min_risk"]; links_enabled?: boolean; locale?: NotifySettings["locale"];
  /** "" removes the webhook; undefined keeps it. */
  slack_webhook?: string; teams_webhook?: string;
}

export type CapState = "ok" | "warn" | "exhausted";
export interface PlanUsage {
  plan: "free" | "paid"; hard: boolean; cap_usd: number; spent_usd: number; reserved_usd: number; estimated_usd: number;
  remaining_usd: number; percent_used: number; state: CapState; paused: boolean; operator_cap_usd?: number;
}

export interface DailyGoal {
  date: string; id: string; target: number; progress: number; reward: number; done: boolean; reached: boolean; enabled: boolean; resets_at: string;
}

/** A notification that ran out of attempts (outbox dead letter). The payload never leaves the server. */
export interface DeadLetter { id: string; channel: "email" | "slack" | "teams"; recipient?: string; attempts: number; last_error: string; created_at: string }

export const notifyApi = {
  settings: () => call<NotifySettings>("GET", "/notify/settings"),
  save: (b: NotifyInput) => call<NotifySettings>("PUT", "/notify/settings", b),
  test: () => call<{ sent: Record<string, "ok" | "failed"> }>("POST", "/notify/test"),
  planUsage: () => call<PlanUsage>("GET", "/plan/usage"),
  deadLetters: () => call<{ items: DeadLetter[]; enabled: boolean }>("GET", "/notify/outbox/dead"),
  retryDead: (id: string) => call<{ id: string; status: string }>("POST", `/notify/outbox/dead/${encodeURIComponent(id)}/retry`),
  dailyGoal: () => call<DailyGoal>("GET", "/rewards/daily-goal"),
};
