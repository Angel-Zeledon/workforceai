import { API_URL, MOCK } from "@/lib/config";
import { authFetch } from "@/lib/session";

/** Contract: backend/internal/governance/http.go and docs/architecture/governance.md. */

export class GovError extends Error {
  constructor(public status: number, message: string, public retryAfter = 0) { super(message); }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) throw new GovError(404, "not available in mock mode");
  const res = await authFetch(`${API_URL}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const text = await res.text();
  let json: Record<string, unknown> = {};
  try { json = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) {
    const e = json.error;
    const msg = typeof e === "string" ? e : typeof e === "object" && e && "message" in e ? String((e as { message: unknown }).message) : `${method} ${path} -> ${res.status}`;
    throw new GovError(res.status, msg, Number(res.headers.get("Retry-After") ?? 0));
  }
  return json as T;
}

/** Saves a response as a file with the access token (a plain link could not carry it). Returns the response headers. */
export async function download(path: string, fallbackName: string): Promise<Headers> {
  if (MOCK) throw new GovError(404, "not available in mock mode");
  const res = await authFetch(`${API_URL}${path}`, { cache: "no-store" });
  if (!res.ok) {
    let msg = `${path} -> ${res.status}`;
    try { const j = (await res.json()) as { error?: unknown; code?: string }; if (typeof j.error === "string") msg = j.error; } catch { /* not JSON */ }
    throw new GovError(res.status, msg, Number(res.headers.get("Retry-After") ?? 0));
  }
  const blob = await res.blob();
  const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? fallbackName;
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url; a.download = name;
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
  return res.headers;
}

export interface AgentSpend { id: string; name: string; role: string; department: string; month_spend_usd: number; cap_usd: number }
export interface DepartmentSpend { name: string; agents: number; month_spend_usd: number; limit_usd: number; used_pct: number; over: boolean }
export interface RetentionPolicy {
  enabled: boolean; chats_days: number; files_days: number; audit_days: number; last_run_at?: string; last_result?: PurgeResult;
}
export interface PurgeResult { dry_run: boolean; at: string; messages: number; conversations: number; files_trashed: number; audit_skipped: boolean; audit_entries?: number; capped: boolean }
export interface Overview { month_start: string; agents: AgentSpend[]; departments: DepartmentSpend[]; retention: RetentionPolicy; tokens_active: number; webhooks: number }
export interface UsageRow { key: string; label: string; department?: string; requests: number; calls: number; input_tokens: number; output_tokens: number; cost_usd: number; share_pct: number }
export interface UsageReport { by: string; from: string; to: string; rows: UsageRow[]; total: UsageRow }
export interface ApiToken { id: string; name: string; prefix: string; scopes: string[]; created_by: string; created_at: string; expires_at?: string; revoked_at?: string; last_used_at?: string }
export interface WebhookEndpoint { id: string; url: string; description: string; events: string[]; active: boolean; disabled_reason?: string; secret_hint: string; failure_streak: number; created_at: string }
export interface WebhookDelivery { id: string; endpoint_id: string; event: string; status: string; attempts: number; next_attempt_at: string; last_status: number; last_error: string; created_at: string; delivered_at?: string }
export interface AuditRecord { seq: number; id: string; ts: string; actor: string; action: string; entity: string; entity_id: string; hash: string }
export interface ChainReport { ok: boolean; checked: number; head_seq: number; head_hash: string; broken_at_seq?: number; reason?: string; detail?: string }

const qs = (o: Record<string, string | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const gov = {
  overview: () => req<Overview>("GET", "/governance/overview"),
  policies: () => req<Record<string, unknown>>("GET", "/governance/policies"),
  setDepartment: (agentId: string, department: string) => req("PUT", `/governance/agents/${encodeURIComponent(agentId)}/department`, { department }),
  setDepartmentLimit: (department: string, monthly_usd: number) => req("PUT", "/governance/departments/limit", { department, monthly_usd }),
  setAgentCap: (agentId: string, monthly_budget_usd: number) => req("PUT", `/agents/${encodeURIComponent(agentId)}/budget`, { monthly_budget_usd }),
  usage: (by: string, from?: string, to?: string) => req<UsageReport>("GET", `/governance/reports/usage${qs({ by, from, to })}`),
  usageCsvPath: (by: string, from?: string, to?: string) => `/governance/reports/usage${qs({ by, from, to, format: "csv" })}`,
  auditPage: (f: AuditFilters) => req<{ items: AuditRecord[]; next_cursor?: string }>("GET", `/audit${qs({ actor: f.actor, type: f.type, from: f.from, to: f.to, limit: "20" })}`),
  auditVerify: () => req<ChainReport>("GET", "/audit/verify"),
  auditExportPath: (f: AuditFilters, format: string) => `/governance/audit/export${qs({ actor: f.actor, type: f.type, from: f.from, to: f.to, format })}`,
  retention: () => req<{ policy: RetentionPolicy; min_days: Record<string, number>; max_days: number; audit_purge_supported: boolean }>("GET", "/governance/retention"),
  putRetention: (p: Pick<RetentionPolicy, "enabled" | "chats_days" | "files_days" | "audit_days">) => req<{ policy: RetentionPolicy }>("PUT", "/governance/retention", p),
  previewRetention: () => req<PurgeResult>("POST", "/governance/retention/preview", {}),
  runRetention: () => req<PurgeResult>("POST", "/governance/retention/run", { confirm: true }),
  tokens: () => req<{ items: ApiToken[]; scopes: string[] }>("GET", "/governance/tokens"),
  createToken: (name: string, scopes: string[], expires_in_days: number) => req<{ token: string; item: ApiToken }>("POST", "/governance/tokens", { name, scopes, expires_in_days }),
  revokeToken: (id: string) => req("DELETE", `/governance/tokens/${encodeURIComponent(id)}`),
  webhooks: () => req<{ items: WebhookEndpoint[]; events: string[] }>("GET", "/governance/webhooks"),
  createWebhook: (url: string, description: string, events: string[]) => req<{ item: WebhookEndpoint; secret: string }>("POST", "/governance/webhooks", { url, description, events }),
  deleteWebhook: (id: string) => req("DELETE", `/governance/webhooks/${encodeURIComponent(id)}`),
  setWebhookActive: (id: string, active: boolean) => req("PUT", `/governance/webhooks/${encodeURIComponent(id)}/active`, { active }),
  testWebhook: (id: string) => req<WebhookDelivery>("POST", `/governance/webhooks/${encodeURIComponent(id)}/test`, {}),
  deliveries: (id: string) => req<{ items: WebhookDelivery[] }>("GET", `/governance/webhooks/${encodeURIComponent(id)}/deliveries?limit=15`),
  members: () => req<{ members: Member[] }>("GET", "/auth/members"),
  setRole: (userId: string, role: string) => req("PATCH", `/auth/members/${encodeURIComponent(userId)}`, { role }),
};

export interface Member { user_id: string; email: string; name: string; role: string; created_at: string }
export interface AuditFilters { actor?: string; type?: string; from?: string; to?: string }
