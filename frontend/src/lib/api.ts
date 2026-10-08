import { API_URL, MOCK } from "./config";
import type {
  Agent, AgentDetail, Approval, ChatMessage, Conversation, Message, Metrics, Report, Request, Task, ActivityItem,
} from "./types";

export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) {
    const { mockBackend } = await import("./mock/engine");
    return (await mockBackend.handle(method, path, body)) as T;
  }
  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status}`);
  const text = await res.text();
  return (text ? JSON.parse(text) : {}) as T;
}
function list<T>(x: any): T[] {
  return Array.isArray(x) ? x : Array.isArray(x?.items) ? x.items : [];
}

/** Normalizes a history row from GET /conversations/{id}/messages (accepts `conversation` or `conversation_id`). */
export function toChatMessage(x: any, conv: string): ChatMessage {
  return {
    id: String(x.id ?? x.message_id ?? `${x.turn_id ?? "t"}:${x.from}:${x.ts}`),
    conversation: String(x.conversation ?? x.conversation_id ?? conv),
    turn_id: x.turn_id ?? null, from: String(x.from), to: String(x.to ?? ""),
    kind: x.kind ?? "chat", text: String(x.text ?? ""), reply_to: x.reply_to ?? null, ts: String(x.ts ?? new Date().toISOString()), handoff_to: x.handoff_to ?? null,
  };
}

/** GET /role-templates item (the fields the UI uses). Contract: backend/internal/api/roles.go. */
export interface RoleTemplate {
  id: string; version: number; category: string; risk_tier: string; seed: boolean;
  title: string; description: string; responsibilities: string[]; disclaimers: string[]; out_of_scope: string[];
  display: { color: string; appearance: { skin: string; hair: string; hair_style: string; accessory: string; tie: boolean; female: boolean } };
  tools: string[]; autonomy: { default: string; ceiling: string }; hired: number;
}

export const api = {
  roleTemplates: async (locale: string) => list<RoleTemplate>(await call("GET", `/role-templates?locale=${encodeURIComponent(locale)}`)),
  hireFromTemplate: (template_id: string, locale: string, name?: string) =>
    call<{ agent: Agent; template_id: string }>("POST", "/agents/from-template", { template_id, locale, ...(name?.trim() ? { name: name.trim() } : {}) }),
  agents: async () => list<Agent>(await call("GET", "/agents")),
  agentDetail: (id: string) => call<AgentDetail>("GET", `/agents/${id}/detail`),
  tasks: async () => list<Task>(await call("GET", "/tasks")),
  requests: async () => list<Request>(await call("GET", "/requests")),
  conversations: async () => list<Conversation>(await call("GET", "/conversations")),
  messages: async (id: string) => list<Message>(await call("GET", `/conversations/${id}/messages`)),
  /** Chat history of "office" or "agent:<id>" (newest page; `before` is an ISO ts cursor). */
  chatHistory: async (conv: string, before?: string, limit = 60) => {
    const q = new URLSearchParams({ limit: String(limit), ...(before ? { before } : {}) });
    return list<any>(await call("GET", `/conversations/${conv}/messages?${q}`)).map((x) => toChatMessage(x, conv));
  },
  /** POST /messages: the backend decides who answers and replies over the WebSocket (chat.message / chat.typing / route.decided). */
  postMessage: (conversation: string, text: string) =>
    call<{ message_id: string; turn_id: string }>("POST", "/messages", { conversation, text }),
  approvals: async () => list<Approval>(await call("GET", "/approvals")),
  reports: async () => list<Report>(await call("GET", "/reports")),
  report: (id: string) => call<Report>("GET", `/reports/${id}`),
  activity: async () => list<ActivityItem>(await call("GET", "/activity?limit=100")),
  metrics: () => call<Metrics>("GET", "/metrics"),
  postRequest: (text: string) => call<{ request_id: string }>("POST", "/requests", { text }),
  sendMessage: (convId: string, text: string) => call<unknown>("POST", `/conversations/${convId}/messages`, { text }),
  decide: (id: string, decision: "approve" | "reject", note?: string) =>
    call<unknown>("POST", `/approvals/${id}/decision`, { decision, note }),
  reset: () => call<unknown>("POST", "/demo/reset"),
};
