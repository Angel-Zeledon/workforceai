import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import type { CreatedDataInbox, DataInbox, DataInboxInput, InboxPush } from "./dataInboxLogic";

/** Admin client of the data inboxes. Contract: backend/internal/api/inboxes.go (org:manage). */

/** A failed call: `code` is the server's stable error code (invalid, conflict, not_found, ...). */
export class InboxApiError extends Error {
  constructor(public code: string, public status: number, public detail = "") { super(code); }
}

async function http<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: any = {};
  try { data = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) throw new InboxApiError(String(data?.code || "request_failed"), res.status, String(data?.error || ""));
  return data as T;
}

const mock = async () => (await import("./mock/data-inboxes-mock")).dataInboxesMock;
const enc = encodeURIComponent;

export const dataInboxesApi = {
  list: async (): Promise<DataInbox[]> => (MOCK ? (await mock()).list() : http("GET", "/data-inboxes")),
  create: async (b: DataInboxInput): Promise<CreatedDataInbox> => (MOCK ? (await mock()).create(b) : http("POST", "/data-inboxes", b)),
  update: async (id: string, b: DataInboxInput): Promise<DataInbox> => (MOCK ? (await mock()).update(id, b) : http("PUT", `/data-inboxes/${enc(id)}`, b)),
  rotate: async (id: string): Promise<CreatedDataInbox> => (MOCK ? (await mock()).rotate(id) : http("POST", `/data-inboxes/${enc(id)}/rotate`)),
  revoke: async (id: string): Promise<DataInbox> => (MOCK ? (await mock()).revoke(id) : http("DELETE", `/data-inboxes/${enc(id)}`)),
  pushes: async (id: string, limit = 20): Promise<InboxPush[]> => (MOCK ? (await mock()).pushes(id) : http("GET", `/data-inboxes/${enc(id)}/pushes?limit=${limit}`)),
};
