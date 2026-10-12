// Pure logic of the chat history (no React, no path aliases: unit-testable with node --test).
// Mirrors the backend definition (backend/internal/application/chat_history.go): there is ONE copy of
// every message. The General stream is every message of the office channel plus every 1:1 chat; the
// thread of an agent is a view over the same messages: its own 1:1 chat, every message it sent or
// received, and the user messages and system notices of the turns in which it took part.
// The mock backend and the live (WebSocket) tail of the screen use these same functions.

export const OFFICE = "office";
export const AGENT_PREFIX = "agent:";
export const GENERAL = "general";

export interface ThreadMsg {
  id: string; conversation: string; turn_id: string | null; from: string; to: string; ts: string;
  agent_ids?: string[];
}

export const isChatConv = (c: string) => c === OFFICE || c.startsWith(AGENT_PREFIX);
export const isAgentActor = (id: string) => !!id && id !== "user" && id !== "system" && id !== "all";

/** Turn ids in which the agent sent or received a chat message. */
export function agentTurns(msgs: ThreadMsg[], agent: string): Set<string> {
  const out = new Set<string>();
  for (const m of msgs) if (isChatConv(m.conversation) && m.turn_id && (m.from === agent || m.to === agent)) out.add(m.turn_id);
  return out;
}

/** turn id -> agents that took part in it (in order of appearance). */
export function turnAgents(msgs: ThreadMsg[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const m of msgs) {
    if (!m.turn_id || !isChatConv(m.conversation)) continue;
    const list = out.get(m.turn_id) ?? [];
    for (const p of [m.from, m.to]) if (isAgentActor(p) && !list.includes(p)) list.push(p);
    out.set(m.turn_id, list);
  }
  return out;
}

/** Does message m belong to the thread of `agent` (General when agent is "")? */
export function inThread(m: ThreadMsg, agent: string, turns: Set<string>): boolean {
  if (!isChatConv(m.conversation)) return false;
  if (!agent) return true;
  if (m.conversation === AGENT_PREFIX + agent || m.from === agent || m.to === agent) return true;
  return (m.from === "user" || m.from === "system") && !!m.turn_id && turns.has(m.turn_id);
}

/** The agents a message belongs to (the badges): who sent it, who took part in its turn, who it was addressed to. */
export function agentBadges(m: ThreadMsg, turns: Map<string, string[]>): string[] {
  const ids: string[] = [];
  const add = (p: string) => { if (isAgentActor(p) && !ids.includes(p)) ids.push(p); };
  add(m.from);
  for (const p of (m.turn_id && turns.get(m.turn_id)) || []) add(p);
  for (const p of m.agent_ids || []) add(p);
  add(m.to);
  if (m.conversation.startsWith(AGENT_PREFIX)) add(m.conversation.slice(AGENT_PREFIX.length));
  return ids;
}

/** Messages of a thread ("general" or an agent id) out of every known chat message, oldest first. */
export function threadOf<T extends ThreadMsg>(all: T[], thread: string): T[] {
  const agent = thread === GENERAL ? "" : thread;
  const turns = agent ? agentTurns(all, agent) : new Set<string>();
  return all.filter((m) => inThread(m, agent, turns)).sort(byTime);
}

export const byTime = (a: { ts: string; id: string }, b: { ts: string; id: string }) => (+new Date(a.ts) - +new Date(b.ts)) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

/** Merges by id (later wins) and drops the optimistic local echo once the real message arrived. */
export function mergeById<T extends ThreadMsg & { text: string }>(...lists: T[][]): T[] {
  const map = new Map<string, T>();
  for (const l of lists) for (const m of l) map.set(m.id, { ...map.get(m.id), ...m });
  const all = Array.from(map.values()).sort(byTime);
  return all.filter((m) => !(m.id.startsWith("local:") && all.some((o) => o !== m && !o.id.startsWith("local:") && o.from === m.from && o.text === m.text && o.turn_id)));
}

/** Groups messages by calendar day (local) for the date separators. */
export function dayKey(ts: string): string {
  const d = new Date(ts);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

// ---------------- conversation list ----------------

export type ConvKind = "office" | "agent" | "request";
export const convKind = (id: string): ConvKind => (id === OFFICE ? "office" : id.startsWith(AGENT_PREFIX) ? "agent" : "request");

export interface ConvRowLike { id: string; title: string; kind: string; archived: boolean; updated_at: string; request_id: string | null }

/** Past conversations shown under the agents: the request conversations, newest first. */
export function pastConversations<T extends ConvRowLike>(rows: T[], showArchived: boolean): T[] {
  return rows.filter((r) => r.kind === "request" && r.archived === showArchived).sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
}

/** One-line preview of a message text (collapses whitespace). */
export function preview(s: string, max = 90): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > max ? t.slice(0, max - 1) + "…" : t;
}

/** Case-insensitive substring search, escaped (what the backend does with ILIKE). */
export function matches(hay: string, needle: string): boolean {
  return !needle || hay.toLowerCase().includes(needle.toLowerCase());
}

/** Splits text around the query for highlighting; the query is matched literally. */
export function highlightParts(text: string, q: string): { text: string; hit: boolean }[] {
  const needle = q.trim();
  if (!needle) return [{ text, hit: false }];
  const out: { text: string; hit: boolean }[] = [];
  const low = text.toLowerCase(), n = needle.toLowerCase();
  let i = 0;
  for (;;) {
    const j = low.indexOf(n, i);
    if (j < 0) break;
    if (j > i) out.push({ text: text.slice(i, j), hit: false });
    out.push({ text: text.slice(j, j + n.length), hit: true });
    i = j + n.length;
  }
  if (i < text.length) out.push({ text: text.slice(i), hit: false });
  return out.length ? out : [{ text, hit: false }];
}

/** Path of a thread: "/chats" or "/chats/<agent>". */
export const threadPath = (thread: string) => (thread === GENERAL ? "/chats" : `/chats/${encodeURIComponent(thread)}`);
/** Thread key of a pathname (General when it is not an agent path). */
export function threadFromPath(pathname: string): string {
  const m = /^\/chats\/([^/]+)\/?$/.exec(pathname);
  return m ? decodeURIComponent(m[1]) : GENERAL;
}
/** Conversation id a composer writes to for a thread. */
export const convForThread = (thread: string) => (thread === GENERAL ? OFFICE : AGENT_PREFIX + thread);
