import { API_URL, MOCK } from "./config";
import type {
  Agent, AgentDetail, Approval, Conversation, Message, Metrics, Report, Request, Task, ActivityItem,
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

export const api = {
  agents: async () => list<Agent>(await call("GET", "/agents")),
  agentDetail: (id: string) => call<AgentDetail>("GET", `/agents/${id}/detail`),
  tasks: async () => list<Task>(await call("GET", "/tasks")),
  requests: async () => list<Request>(await call("GET", "/requests")),
  conversations: async () => list<Conversation>(await call("GET", "/conversations")),
  messages: async (id: string) => list<Message>(await call("GET", `/conversations/${id}/messages`)),
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
