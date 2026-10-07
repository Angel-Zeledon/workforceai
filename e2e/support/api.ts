import { API_URL } from "./env";

export interface Approval {
  id: string;
  task_id: string;
  agent_id: string;
  action: string;
  title: string;
  status: "pending" | "approved" | "rejected";
}

export interface RequestRow {
  id: string;
  status: "planning" | "running" | "awaiting_approval" | "done" | "failed";
  report_id: string | null;
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw new Error(`${res.url} -> ${res.status} ${await res.text()}`);
  return (await res.json()) as T;
}

export const api = {
  get: async <T>(path: string): Promise<T> => json<T>(await fetch(`${API_URL}${path}`)),
  post: async <T>(path: string, body?: unknown): Promise<T> =>
    json<T>(
      await fetch(`${API_URL}${path}`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      }),
    ),
};

export const resetDemo = () => api.post("/demo/reset");

export async function pendingApprovals(): Promise<Approval[]> {
  return (await api.get<Approval[] | null>("/approvals?status=pending")) ?? [];
}

export async function sleep(ms: number) {
  await new Promise((r) => setTimeout(r, ms));
}

/** Aprueba por API todas las aprobaciones que aparezcan hasta que la solicitud termine. */
export async function approveUntilDone(requestId: string, timeoutMs = 180_000): Promise<RequestRow> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    for (const a of await pendingApprovals()) {
      await api.post(`/approvals/${a.id}/decision`, { decision: "approve" });
    }
    const r = await api.get<RequestRow>(`/requests/${requestId}`);
    if (r.status === "done" || r.status === "failed") return r;
    await sleep(1000);
  }
  throw new Error(`Timeout esperando request ${requestId}`);
}
