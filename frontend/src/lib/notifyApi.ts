import { call } from "./api";
import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";

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
/** "paid" is the legacy "no plan" of a deployment without plans (no cap, no plan limits). */
export type PlanId = "free" | "pro" | "max" | "paid";
export interface PlanUsage {
  plan: PlanId; hard: boolean; cap_usd: number; spent_usd: number; reserved_usd: number; estimated_usd: number;
  remaining_usd: number; percent_used: number; state: CapState; paused: boolean; operator_cap_usd?: number;
}

/** One plan of the catalog (backend/internal/policy/plans.go). A zero count = no plan limit. */
export interface PlanTier {
  id: PlanId; price_monthly_usd: number; price_yearly_usd: number; ai_allowance_usd: number;
  max_agents: number; max_active_projects: number; max_schedules: number; max_seats: number; storage_bytes: number;
}
/** What the organization has of each counted limit; -1 = not known on this deployment. */
export interface PlanUsed { agents: number; active_projects: number; schedules: number; seats: number; storage_bytes: number }
/** GET /plan (backend/internal/api/plan.go). */
export interface PlanView { plan: PlanId; limits: PlanTier; used: PlanUsed; usage: PlanUsage; catalog: PlanTier[]; self_serve: boolean }

export class PlanError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

async function planCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) throw new PlanError(404, "not available in mock mode");
  const res = await authFetch(`${API_URL}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const text = await res.text();
  let json: Record<string, unknown> = {};
  try { json = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) throw new PlanError(res.status, String(json.error ?? `${method} ${path} -> ${res.status}`));
  return json as T;
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
  /** The plan, its limits, what is used of each and the catalog. */
  orgPlan: () => planCall<PlanView>("GET", "/plan"),
  /** Owners only, and only when the deployment has PLAN_SELF_SERVE on (403 otherwise). */
  changePlan: (plan: PlanId) => planCall<PlanView>("PUT", "/plan", { plan }),
  deadLetters: () => call<{ items: DeadLetter[]; enabled: boolean }>("GET", "/notify/outbox/dead"),
  retryDead: (id: string) => call<{ id: string; status: string }>("POST", `/notify/outbox/dead/${encodeURIComponent(id)}/retry`),
  dailyGoal: () => call<DailyGoal>("GET", "/rewards/daily-goal"),
};
