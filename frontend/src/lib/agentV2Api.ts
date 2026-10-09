import { API_URL, MOCK } from "./config";
import { call } from "./api";
import { authFetch } from "./session";
import type { Creativity, FieldDiff, ProfileLimits, PromptSegment } from "./agentV2Logic";

/** Contract: V2 agent profile / thought audit / intervention endpoints (all under /api/v1). */

export interface AgentProfile {
  agent_id: string; instructions: string; creativity: Creativity; tone: string; skills: string[]; role_skills: string[];
  version: number; updated_by: string; updated_at: string | null; limits: ProfileLimits;
}
export interface ProfileInput { instructions: string; creativity: Creativity; skills: string[]; tone?: string }
export interface ProfileVersion {
  version: number; author: string; created_at: string; restored_from: number | null;
  diff: Record<string, FieldDiff>; snapshot: { instructions: string; creativity: Creativity; skills: string[]; tone: string };
}

export interface ToolRequest {
  tool: string; action: string; risk: string; decision: "allow" | "require_approval" | "deny" | ""; rule_id: string; reason: string;
  approval_status: "" | "pending" | "approved" | "rejected";
}
export interface TraceEvent {
  ts: string; type: "tool_decision" | "approval" | "review" | "retry" | "steer" | "apply_now" | "reassign"; data: Record<string, unknown>;
}
export interface Trace {
  id: string; task_id: string; request_id: string; kind: string; attempt: number; created_at: string;
  provider: string; model: string; input_tokens: number; output_tokens: number; cost_usd: number; latency_ms: number;
  status: "ok" | "error"; error?: string; rationale: string;
  output_summary: { summary: string; confidence: number; findings: number; recommendations: number };
  tool_requests: ToolRequest[]; events: TraceEvent[]; steer_notes_applied: number; prompt_stored: boolean; truncated: boolean;
  prompt?: { system: string; segments: PromptSegment[] };
}
export interface TraceList { traces: Trace[]; store_prompts: boolean; retention_days: number }
export interface TraceSettings { store_prompts: boolean; retention_days: number }

export interface SteerNote { id: string; note: string; author: string; created_at: string; delivered_at: string | null }
export interface ReassignCandidate { id: string; name: string; role: string; title: string }
export interface Intervention {
  task_status: string; steer_notes: SteerNote[]; can_steer: boolean; can_apply_now: boolean; can_reassign: boolean;
  estimated_rerun_usd: number; reassign_candidates: ReassignCandidate[];
}
export interface SteerResult { note: SteerNote; apply_now_status: "" | "restarted" | "not_mid_call"; estimated_rerun_usd: number }

export const agentV2Api = {
  profile: (id: string) => call<AgentProfile>("GET", `/agents/${id}/profile`),
  putProfile: (id: string, b: ProfileInput) => call<AgentProfile>("PUT", `/agents/${id}/profile`, b),
  profileVersions: async (id: string) => {
    const r = await call<ProfileVersion[] | { items?: ProfileVersion[] }>("GET", `/agents/${id}/profile/versions`);
    return Array.isArray(r) ? r : r.items ?? [];
  },
  restoreProfile: (id: string, version: number) => call<AgentProfile>("POST", `/agents/${id}/profile/restore`, { version }),

  traces: (id: string, limit = 50, before?: string) => {
    const q = new URLSearchParams({ limit: String(limit), ...(before ? { before } : {}) });
    return call<TraceList>("GET", `/agents/${id}/traces?${q}`);
  },
  trace: (id: string, traceId: string) => call<Trace>("GET", `/agents/${id}/traces/${encodeURIComponent(traceId)}`),
  traceSettings: () => call<TraceSettings>("GET", "/settings/traces"),
  putTraceSettings: (store_prompts: boolean) => call<{ store_prompts: boolean }>("PUT", "/settings/traces", { store_prompts }),

  intervention: (taskId: string) => call<Intervention>("GET", `/tasks/${taskId}/intervention`),
  steer: (taskId: string, note: string, apply_now: boolean) => call<SteerResult>("POST", `/tasks/${taskId}/steer`, { note, apply_now }),
  reassign: (taskId: string, agent_id: string) => call<unknown>("POST", `/tasks/${taskId}/reassign`, { agent_id }),
};

/** Downloads GET /agents/{id}/traces/export (audited server-side) as a file via an authenticated fetch. */
export async function downloadTraceExport(agentId: string): Promise<void> {
  if (MOCK) throw new Error("GET export -> 404");
  const res = await authFetch(`${API_URL}/agents/${agentId}/traces/export`, { cache: "no-store" });
  if (!res.ok) throw new Error(`GET /agents/${agentId}/traces/export -> ${res.status}`);
  const blob = await res.blob();
  const cd = res.headers.get("Content-Disposition") || "";
  const name = /filename="?([^";]+)"?/.exec(cd)?.[1] || `agent-${agentId}-traces.json`;
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url; a.download = name;
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
