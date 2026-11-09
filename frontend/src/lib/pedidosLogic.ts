// Pure logic of "pedidos" (every piece of work someone asked for), shown beside the chat (no React, no path
// aliases: unit-testable with node --test). Spec: docs/superpowers/specs/2026-11-03-chats-y-pedidos-design.md.
// A pedido is a request (its id is the request id) plus its internal conversation (the conversation of kind
// "request", where archive / rename / search already work).

export type PedidoStatus = "running" | "needs_help" | "needs_approval" | "paused" | "done" | "failed" | "unknown";

/** Status of a pedido out of its request, its tasks, its open questions ("needs your help") and pending approvals. */
export function pedidoStatus(req: { status: string } | undefined, tasks: { status: string }[], openQuestions: number, pendingApprovals: number): PedidoStatus {
  if (!req) return "unknown";
  if (req.status === "failed") return "failed";
  if (req.status === "done") return "done";
  if (openQuestions > 0 || tasks.some((t) => t.status === "needs_input")) return "needs_help";
  if (req.status === "awaiting_approval" || req.status === "awaiting_confirmation" || pendingApprovals > 0 || tasks.some((t) => t.status === "awaiting_approval")) return "needs_approval";
  if (req.status === "paused") return "paused";
  return "running";
}

/** The person has to do something (answer or approve): the card and the row say so and point to Pendientes. */
export const needsAttention = (s: PedidoStatus) => s === "needs_help" || s === "needs_approval";

const isAgent = (id: string) => !!id && id !== "user" && id !== "system" && id !== "all";

/** Who does it: the agents of its tasks (in order), else the conversation participants without the people. */
export function pedidoAgents(tasks: { agent_id: string }[], participants: string[] = []): string[] {
  const out: string[] = [];
  for (const t of tasks) if (isAgent(t.agent_id) && !out.includes(t.agent_id)) out.push(t.agent_id);
  if (out.length) return out;
  for (const p of participants) if (isAgent(p) && !out.includes(p)) out.push(p);
  return out;
}

export interface PedidoConvLike {
  id: string; title: string; kind: string; request_id: string | null; project_id: string | null;
  archived: boolean; unread: number; updated_at: string; participants?: string[];
}
export interface PedidoCtx {
  requests: Record<string, { status: string } | undefined>;
  tasks: { request_id: string; agent_id: string; status: string; task_id?: string; id?: string }[];
  questions: { request_id: string; status: string }[];
  approvals: { task_id: string; status: string }[];
}
export interface PedidoRow {
  id: string; convId: string; title: string; status: PedidoStatus; agents: string[];
  updatedAt: string; unread: number; archived: boolean; projectId: string | null;
}

/** Status and agents of one request id, out of the store-like context. */
export function pedidoInfo(requestId: string, ctx: PedidoCtx, participants: string[] = []): { status: PedidoStatus; agents: string[] } {
  const tasks = ctx.tasks.filter((t) => t.request_id === requestId);
  const taskIds = new Set(tasks.map((t) => t.id).filter(Boolean) as string[]);
  const qs = ctx.questions.filter((q) => q.request_id === requestId && q.status === "open").length;
  const aps = ctx.approvals.filter((a) => a.status === "pending" && taskIds.has(a.task_id)).length;
  return { status: pedidoStatus(ctx.requests[requestId], tasks, qs, aps), agents: pedidoAgents(tasks, participants) };
}

/** The "Pedidos" group of the sidebar: the request conversations (archived or not), the ones needing you first, then newest. */
export function pedidoRows(convs: PedidoConvLike[], ctx: PedidoCtx, o: { archived: boolean }): PedidoRow[] {
  // One row per pedido: a pedido can have more than one conversation (or the same one twice after "load more");
  // the newest conversation opens, and unread messages add up across different conversations.
  const byId = new Map<string, PedidoRow>();
  const seen = new Set<string>();
  for (const c of convs) {
    if (c.kind !== "request" || !c.request_id || c.archived !== o.archived) continue;
    const prev = byId.get(c.request_id);
    const unread = (seen.has(c.id) ? 0 : c.unread ?? 0) + (prev?.unread ?? 0);
    seen.add(c.id);
    if (prev && +new Date(prev.updatedAt) >= +new Date(c.updated_at)) { prev.unread = unread; continue; }
    const { status, agents } = pedidoInfo(c.request_id, ctx, c.participants);
    byId.set(c.request_id, { id: c.request_id, convId: c.id, title: c.title, status, agents, updatedAt: c.updated_at, unread, archived: c.archived, projectId: c.project_id });
  }
  return [...byId.values()].sort((a, b) => (Number(needsAttention(b.status)) - Number(needsAttention(a.status))) || (+new Date(b.updatedAt) - +new Date(a.updatedAt)));
}

/** One card per pedido in a chat: on the latest message that belongs to it. */
export function pedidoCardAnchors(msgs: { id: string; request_id?: string | null }[]): Set<string> {
  const last = new Map<string, string>();
  for (const m of msgs) if (m.request_id) last.set(m.request_id, m.id);
  return new Set(last.values());
}

/** Lowercase, no accents, single spaces (how every search of the app compares). */
export const foldText = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().trim().replace(/\s+/g, " ");

export interface SearchHitLike { id: string; kind: string; agent_id?: string; request_id: string | null }

/** The search box looks in the three groups at once: agents, people and pedidos (local names + the backend hits by text). */
export function searchGroups(
  q: string,
  src: { agents: { key: string; name: string; sub: string }[]; people: { id: string; title: string; sub: string }[]; pedidos: { id: string; title: string }[]; hits: SearchHitLike[] },
): { agents: string[]; people: string[]; pedidos: string[] } {
  const n = foldText(q);
  const out = { agents: [] as string[], people: [] as string[], pedidos: [] as string[] };
  if (!n) return out;
  const add = (l: string[], id: string | null | undefined) => { if (id && !l.includes(id)) l.push(id); };
  const has = (...s: string[]) => s.some((x) => foldText(x || "").includes(n));
  for (const a of src.agents) if (has(a.name, a.sub)) add(out.agents, a.key);
  for (const p of src.people) if (has(p.title, p.sub)) add(out.people, p.id);
  for (const p of src.pedidos) if (has(p.title)) add(out.pedidos, p.id);
  for (const h of src.hits) {
    if (h.kind === "office") add(out.agents, "general");
    else if (h.kind === "agent") add(out.agents, h.agent_id || (h.id.startsWith("agent:") ? h.id.slice(6) : ""));
    else if (h.kind === "request") add(out.pedidos, h.request_id);
  }
  return out;
}

/** The open pedido of a location search ("?pedido=<request id>"), and the old link to a work conversation ("?conv=<id>"). */
export function pedidoFromSearch(search: string): { pedido: string; conv: string } {
  const p = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  return { pedido: p.get("pedido") ?? "", conv: p.get("conv") ?? "" };
}

/** URL with the pedido open (id) or closed (null); the old conv link is dropped, every other parameter kept. */
export function pedidoHref(pathname: string, search: string, id: string | null): string {
  const p = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  p.delete("conv");
  p.delete("pedido");
  if (id) p.set("pedido", id);
  const s = p.toString();
  return s ? `${pathname}?${s}` : pathname;
}
