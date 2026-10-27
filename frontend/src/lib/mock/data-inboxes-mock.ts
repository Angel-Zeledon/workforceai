/**
 * Mock of the data inboxes (NEXT_PUBLIC_MOCK=true): in memory, with one sample inbox whose pushes match the file
 * the mock drive seeds under /Shared/Imports/pedidos-erp (authored by the inbox). Nothing is reachable from
 * outside the browser in the demo: the push URL and token are shown so the flow can be seen, not used.
 */
import { InboxApiError } from "../dataInboxesApi";
import type { CreatedDataInbox, DataInbox, DataInboxInput, InboxPush } from "../dataInboxLogic";
import { MAX_NAME, MIN_MAX_BYTES } from "../dataInboxLogic";

const DEMO_ID = "ibx_demo_erp";
const now = () => new Date().toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString();

function randomToken(): string {
  const b = new Uint8Array(32);
  crypto.getRandomValues(b);
  return "aiwi_" + btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
const rid = (p: string) => `${p}${Math.random().toString(16).slice(2, 18).padEnd(16, "0")}`;

function slugify(name: string): string {
  const s = name.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  return s || "inbox";
}

const inboxes = new Map<string, DataInbox>();
const pushes = new Map<string, InboxPush[]>();

function seed() {
  if (inboxes.size) return;
  const folder = "/Shared/Imports/pedidos-erp";
  inboxes.set(DEMO_ID, {
    id: DEMO_ID, name: "Pedidos ERP (diario)", slug: "pedidos-erp", folder, customer_id: "", allowed: ["json", "csv"], max_bytes: 5 << 20, rate_per_min: 30,
    token_hint: "x9Qa", created_by: "user", created_at: ago(60 * 24 * 3), updated_at: ago(60 * 24 * 3), rotated_at: null, revoked_at: null,
    last_push_at: ago(42), push_count: 2, status: "active", folder_path: folder, push_path: `/api/v1/inbox/${DEMO_ID}`, push_url: "",
  });
  pushes.set(DEMO_ID, [
    { id: "psh_demo2", inbox_id: DEMO_ID, status: "done", size: 186, received_at: ago(42),
      files: [{ file_id: "fil_demo_inbox_1", path: `${folder}/orders-2026-10-26.json`, name: "orders-2026-10-26.json", version: 2, size: 186, mime: "application/json", kind: "json", created: false, unchanged: false }] },
    { id: "psh_demo1", inbox_id: DEMO_ID, status: "done", size: 141, received_at: ago(60 * 24 + 42),
      files: [{ file_id: "fil_demo_inbox_1", path: `${folder}/orders-2026-10-26.json`, name: "orders-2026-10-26.json", version: 1, size: 141, mime: "application/json", kind: "json", created: true, unchanged: false }] },
  ]);
}

function get(id: string): DataInbox {
  seed();
  const i = inboxes.get(id);
  if (!i) throw new InboxApiError("not_found", 404);
  return i;
}

function check(b: DataInboxInput) {
  const name = (b.name ?? "").trim();
  if (!name || [...name].length > MAX_NAME) throw new InboxApiError("invalid", 400, "name");
  if (!b.allowed?.length) throw new InboxApiError("invalid", 400, "allowed");
  if (b.max_bytes < MIN_MAX_BYTES || b.max_bytes > 25 << 20) throw new InboxApiError("invalid", 400, "max_bytes");
}

export const dataInboxesMock = {
  list: async (): Promise<DataInbox[]> => { seed(); return [...inboxes.values()].sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)).map((i) => ({ ...i })); },
  create: async (b: DataInboxInput): Promise<CreatedDataInbox> => {
    seed();
    check(b);
    const id = rid("ibx_"), token = randomToken();
    let slug = slugify(b.name);
    if ([...inboxes.values()].some((i) => i.slug === slug)) slug += "-" + id.slice(-4);
    const custFolder = b.customer_id ? "/Clients/Grupo Alfa" : "";
    const rel = (b.folder ?? "").trim().replace(/^\/+|\/+$/g, "");
    const folder = b.customer_id ? (rel || `Imports/${slug}`) : ((b.folder ?? "").trim() || `/Shared/Imports/${slug}`);
    if (!b.customer_id && !/^\/(Shared|Projects\/[^/]+)(\/|$)/.test(folder)) throw new InboxApiError("invalid", 400, "folder");
    const i: DataInbox = {
      id, name: b.name.trim(), slug, folder, customer_id: b.customer_id ?? "", allowed: b.allowed, max_bytes: b.max_bytes, rate_per_min: b.rate_per_min || 30,
      token_hint: token.slice(-4), created_by: "user", created_at: now(), updated_at: now(), rotated_at: null, revoked_at: null,
      last_push_at: null, push_count: 0, status: "active", folder_path: custFolder ? `${custFolder}/${folder}` : folder, push_path: `/api/v1/inbox/${id}`, push_url: "",
    };
    inboxes.set(id, i);
    pushes.set(id, []);
    return { ...i, token };
  },
  update: async (id: string, b: DataInboxInput): Promise<DataInbox> => {
    const i = get(id);
    if (i.status === "revoked") throw new InboxApiError("conflict", 409);
    check(b);
    Object.assign(i, { name: b.name.trim(), allowed: b.allowed, max_bytes: b.max_bytes, rate_per_min: b.rate_per_min || i.rate_per_min, updated_at: now() });
    return { ...i };
  },
  rotate: async (id: string): Promise<CreatedDataInbox> => {
    const i = get(id);
    if (i.status === "revoked") throw new InboxApiError("conflict", 409);
    const token = randomToken();
    Object.assign(i, { token_hint: token.slice(-4), rotated_at: now(), updated_at: now() });
    return { ...i, token };
  },
  revoke: async (id: string): Promise<DataInbox> => {
    const i = get(id);
    if (i.status !== "revoked") Object.assign(i, { status: "revoked", revoked_at: now(), revoked_by: "user" });
    return { ...i };
  },
  pushes: async (id: string): Promise<InboxPush[]> => { get(id); return (pushes.get(id) ?? []).map((p) => ({ ...p })); },
};
