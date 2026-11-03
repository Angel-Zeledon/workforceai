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
/** How the plan changes: paid with ONVO Pay, PUT /plan without charging, or only by the platform's team. */
export type PlanBillingMode = "onvo" | "self_serve" | "none";
/** The ONVO subscription the plan comes from (billing "onvo"). */
export interface PlanSubscription { subscribed: boolean; cancel_at_period_end: boolean; period_end?: string; past_due: boolean }
export interface PlanView {
  plan: PlanId; limits: PlanTier; used: PlanUsed; usage: PlanUsage; catalog: PlanTier[]; self_serve: boolean;
  billing: PlanBillingMode; subscription?: PlanSubscription;
  /** what can be bought: "pro_monthly", "max_yearly"... */ available?: string[];
}
/** A subscription waiting for its first payment: ONVO's SDK pays it in the browser. */
export interface PlanStarted { subscription_id: string; customer_id: string; publishable_key: string }
export type PlanPeriod = "monthly" | "yearly";

export class PlanError extends Error {
  constructor(public status: number, message: string, public code = "") { super(message); }
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
  if (!res.ok) throw new PlanError(res.status, String(json.error ?? `${method} ${path} -> ${res.status}`), String(json.code ?? ""));
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
  /** ONVO: creates the subscription of a purchase or a plan change; the browser pays it with ONVO's SDK. */
  subscribe: (plan: PlanId, period: PlanPeriod, name: string, email: string) =>
    planCall<PlanStarted>("POST", "/plan/subscribe", { plan, period, name, email }),
  /** ONVO: after paying. 409 code "not_paid" while ONVO is still confirming the payment. */
  confirm: (subscription_id: string) => planCall<PlanView>("POST", "/plan/confirm", { subscription_id }),
  /** ONVO: end the subscription when the paid period ends, or undo that. */
  cancel: (resume: boolean) => planCall<PlanView>("POST", "/plan/cancel", { resume }),
  deadLetters: () => call<{ items: DeadLetter[]; enabled: boolean }>("GET", "/notify/outbox/dead"),
  retryDead: (id: string) => call<{ id: string; status: string }>("POST", `/notify/outbox/dead/${encodeURIComponent(id)}/retry`),
  dailyGoal: () => call<DailyGoal>("GET", "/rewards/daily-goal"),
};
