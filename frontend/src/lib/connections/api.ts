import { call } from "../api";
import type {
  AgentControl, Connection, EmailDraft, Grant, OrgControls, PlanReview, ProviderDef, SpendLimitStatus, UsageRow,
} from "./types";

const items = <T>(x: any): T[] => (Array.isArray(x) ? x : Array.isArray(x?.items) ? x.items : []);

/** Stable error code of a failed call (mock throws the code; the real client throws "METHOD path -> status"). */
export function errCode(e: unknown): string {
  const m = e instanceof Error ? e.message : String(e);
  return /^[a-z_]+$/.test(m) ? m : "request_failed";
}

export interface TestResult { status: "ok" | "failed"; latency_ms: number; account_label: string | null; granted_capabilities: string[]; error_code?: string }
export interface CreateConnectionInput {
  provider: string; kind: "oauth2" | "api_key"; label: string; capabilities: string[];
  resource_scope?: Record<string, string[] | number>;
  expires_at?: string | null;
  oauth_client_id?: string;
  /** WRITE-ONLY. Sent once in this call; callers must not keep it anywhere. */
  oauth_client_secret?: string;
  /** WRITE-ONLY. Sent once in this call; callers must not keep it anywhere. */
  secret?: string;
}

export const connApi = {
  providers: async () => items<ProviderDef>(await call("GET", "/connection-providers")),
  list: async () => items<Connection>(await call("GET", "/connections")),
  create: (b: CreateConnectionInput) => call<Connection>("POST", "/connections", b),
  patch: (id: string, b: Partial<Pick<Connection, "label" | "read_only" | "limits">>) => call<Connection>("PATCH", `/connections/${id}`, b),
  oauthStart: (id: string) => call<{ auth_url: string; expires_at: string }>("POST", `/connections/${id}/oauth/start`),
  /** Mock only: simulates the provider redirect landing on the callback. */
  oauthSimulateCallback: (id: string, denyScopes = false) => call<Connection>("GET", `/connections/oauth/callback?connection_id=${id}${denyScopes ? "&deny_scopes=1" : ""}`),
  rotate: (id: string, secret: string) => call<Connection>("PUT", `/connections/${id}/credential`, { secret }),
  test: (id: string) => call<TestResult>("POST", `/connections/${id}/test`),
  suspend: (id: string) => call<Connection>("POST", `/connections/${id}/suspend`),
  resume: (id: string) => call<Connection>("POST", `/connections/${id}/resume`),
  revoke: (id: string, confirmName: string) => call<Connection>("POST", `/connections/${id}/revoke`, { confirm_name: confirmName }),
  grants: async (id: string) => items<Grant>(await call("GET", `/connections/${id}/grants`)),
  putGrant: (id: string, agentId: string, b: { capabilities: string[]; constraints?: Grant["constraints"]; valid_until?: string | null; confirm_name?: string }) =>
    call<Grant>("PUT", `/connections/${id}/grants/${agentId}`, b),
  approveGrant: (id: string, agentId: string) => call<Grant>("POST", `/connections/${id}/grants/${agentId}`, { approve: true }),
  deleteGrant: (id: string, agentId: string) => call<unknown>("DELETE", `/connections/${id}/grants/${agentId}`),
  usage: async (id: string) => items<UsageRow>(await call("GET", `/connections/${id}/usage`)),
  /** Mock only: an agent "tries" a capability; the policy decides and the result is logged. */
  simulateUse: (id: string, agentId: string, capability: string) => call<UsageRow>("POST", `/connections/${id}/usage/simulate`, { agent_id: agentId, capability }),
  agentGrants: async (agentId: string) => items<Grant>(await call("GET", `/agents/${agentId}/connections`)),
};

export interface OperatingHoursInterval { day: number; open: string; close: string }
export interface OperatingHours { enabled: boolean; timezone: string; weekly: OperatingHoursInterval[]; open_now?: boolean; next_open_at?: string | null }
export interface AnomalySettings { disabled: boolean; auto_freeze: boolean; rules?: string[] }

export const hoursApi = {
  get: () => call<OperatingHours>("GET", "/org/operating-hours"),
  put: (b: Pick<OperatingHours, "enabled" | "timezone" | "weekly">) => call<OperatingHours>("PUT", "/org/operating-hours", b),
  anomaly: () => call<AnomalySettings>("GET", "/org/anomaly-settings"),
  putAnomaly: (b: Pick<AnomalySettings, "disabled" | "auto_freeze">) => call<AnomalySettings>("PUT", "/org/anomaly-settings", b),
};

export const ctlApi = {
  controls: () => call<OrgControls>("GET", "/org/controls"),
  setReadOnly: (on: boolean) => call<OrgControls>("PUT", "/org/controls", { mode: on ? "read_only" : "normal" }),
  setSettings: (settings: Partial<OrgControls["settings"]>) => call<OrgControls>("PUT", "/org/controls", { settings }),
  killSwitch: (level: "freeze" | "lockdown", reason: string) => call<OrgControls>("POST", "/org/controls/kill-switch", { level, reason }),
  setToolDisabled: (tool: string, disabled: boolean) => call<OrgControls>("POST", "/org/controls/tools", { tool, disabled }),
  release: (reason: string, resume: "all" | "none") => call<OrgControls>("POST", "/org/controls/release", { reason, resume_connections: resume }),
  agentControls: async () => items<AgentControl>(await call("GET", "/agent-controls")),
  agentControl: (id: string, action: "pause" | "resume", reason?: string) => call<AgentControl>("POST", `/agents/${id}/control`, { action, drain: "graceful", reason }),
  spend: async () => items<SpendLimitStatus>(await call("GET", "/spend-limits/status")),
  outbox: async () => items<EmailDraft>(await call("GET", "/outbox")),
  editDraft: (id: string, b: Partial<Pick<EmailDraft, "to" | "subject" | "body">>) => call<EmailDraft>("PATCH", `/outbox/${id}`, b),
  approveDraft: (id: string, version: number) => call<EmailDraft>("POST", `/outbox/${id}/approve`, { version }),
  rejectDraft: (id: string) => call<EmailDraft>("POST", `/outbox/${id}/reject`),
  cancelHold: (id: string) => call<EmailDraft>("POST", `/tool-calls/${id}/cancel-hold`),
  plans: async () => items<PlanReview>(await call("GET", "/plan-reviews")),
  patchPlan: (id: string, b: { remove_task_ids?: string[]; note?: string; no_external_actions?: boolean }) => call<PlanReview>("PATCH", `/requests/${id}/plan`, b),
  approvePlan: (id: string) => call<PlanReview>("POST", `/requests/${id}/plan/approve`),
  rejectPlan: (id: string) => call<PlanReview>("POST", `/requests/${id}/plan/reject`),
  /** Mock only. */
  simulatePlan: (text?: string) => call<PlanReview>("POST", "/plan-reviews/simulate", { text }),
  devViewer: (id: string) => call<OrgControls>("POST", "/dev/viewer", { id }),
  devAdmins: (count: 1 | 2) => call<OrgControls>("POST", "/dev/admins", { count }),
};
