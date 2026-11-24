/**
 * Pure logic of the list view as a multi-agent workspace (no React, no path aliases: unit-testable with node --test).
 * The person asks the team for something, sees who is doing it, answers what they are asked and collects what is done.
 */

export type RailGroup = "waiting" | "working" | "free";
export const RAIL_GROUPS: RailGroup[] = ["waiting", "working", "free"];

export interface AgentLike { id: string; name: string; role?: string; state: string; activity?: string }

const WORKING_STATES = new Set(["thinking", "working", "talking", "reviewing", "waiting"]);
const WAITING_STATES = new Set(["awaiting_approval", "blocked"]);

/** Which group an agent is in: waiting for the person (something is theirs, or the agent is stuck), working, or free. */
export function railGroupOf(a: Pick<AgentLike, "state">, needsYou: boolean): RailGroup {
  if (needsYou || WAITING_STATES.has(a.state)) return "waiting";
  if (WORKING_STATES.has(a.state)) return "working";
  return "free";
}

export const fold = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().trim();

export interface RailSection { group: RailGroup; ids: string[] }

/** Agents grouped as Te esperan / Trabajando / Disponibles, in the order of the office; empty groups do not appear. `query` filters by name, role or activity. */
export function groupAgents(order: string[], agents: Record<string, AgentLike | undefined>, needsYouIds: ReadonlySet<string>, query = ""): RailSection[] {
  const q = fold(query);
  const by: Record<RailGroup, string[]> = { waiting: [], working: [], free: [] };
  for (const id of order) {
    const a = agents[id];
    if (!a) continue;
    if (q && !fold(`${a.name} ${a.role ?? ""} ${a.activity ?? ""}`).includes(q)) continue;
    by[railGroupOf(a, needsYouIds.has(id))].push(id);
  }
  return RAIL_GROUPS.filter((g) => by[g].length).map((g) => ({ group: g, ids: by[g] }));
}

/** The agents with something waiting for the person: the owner of every pending approval and open question. */
export function needsYouAgentIds(approvals: { agent_id: string; status: string }[], questions: { agent_id: string; status: string }[]): Set<string> {
  const s = new Set<string>();
  for (const a of approvals) if (a.status === "pending" && a.agent_id) s.add(a.agent_id);
  for (const q of questions) if (q.status === "open" && q.agent_id) s.add(q.agent_id);
  return s;
}

/** The conversation id a target ("all the office" or an agent) writes to. */
export const OFFICE_CONV = "office";
export const convOf = (target: string | null | undefined): string => (!target || target === "all" ? OFFICE_CONV : `agent:${target}`);
/** The target of a conversation id (null = the whole office). */
export const targetOf = (conv: string): string | null => (conv.startsWith("agent:") ? conv.slice(6) : null);
/** A selection that points to an agent that no longer exists falls back to the whole office. */
export const validTarget = (target: string | null, agents: Record<string, unknown>): string | null => (target && agents[target] ? target : null);

export interface NeedItem { kind: "approval" | "question"; id: string; created_at: string }
/** What waits for the person: pending approvals and open questions, the oldest first (it has waited the longest). */
export function needsYouItems(approvals: { id: string; status: string; created_at: string }[], questions: { id: string; status: string; created_at: string; for_me?: boolean }[]): NeedItem[] {
  const out: NeedItem[] = [];
  for (const a of approvals) if (a.status === "pending") out.push({ kind: "approval", id: a.id, created_at: a.created_at });
  for (const q of questions) if (q.status === "open" && q.for_me !== false) out.push({ kind: "question", id: q.id, created_at: q.created_at });
  return out.sort((x, y) => +new Date(x.created_at) - +new Date(y.created_at));
}

export interface TaskLike { id: string; request_id: string; agent_id: string; status: string; title: string; created_at: string; /** a part of a helper group: it is shown by the helpers block, not as a step */ helper?: unknown }
export interface RequestLike { id: string; text: string; status: string; created_at: string }

const OPEN_REQUEST = new Set(["planning", "running", "awaiting_approval", "awaiting_confirmation", "paused"]);
export const isOpenRequest = (status: string) => OPEN_REQUEST.has(status);

export type StepKind = "done" | "active" | "waiting" | "queued" | "failed";
/** How a step reads: finished, being done, waiting for the person, queued behind another, or failed. */
export function stepKind(status: string): StepKind {
  if (status === "done") return "done";
  if (status === "failed") return "failed";
  if (status === "running") return "active";
  if (status === "awaiting_approval" || status === "needs_input" || status === "blocked") return "waiting";
  return "queued";
}

export interface RunStep { id: string; title: string; agent_id: string; kind: StepKind }
export interface RunRow { id: string; text: string; status: string; created_at: string; steps: RunStep[]; done: number; total: number; pct: number; agents: string[] }

/** The requests still in motion with their steps (in the order they were planned), who does each and how far they are; newest first. */
export function inProgress(requests: Record<string, RequestLike>, tasks: TaskLike[], opts: { agent?: string | null } = {}): RunRow[] {
  const rows: RunRow[] = [];
  for (const r of Object.values(requests)) {
    if (!isOpenRequest(r.status)) continue;
    const ts = tasks.filter((t) => t.request_id === r.id && !t.helper).sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at));
    if (opts.agent && !ts.some((t) => t.agent_id === opts.agent)) continue;
    const steps = ts.map((t) => ({ id: t.id, title: t.title, agent_id: t.agent_id, kind: stepKind(t.status) }));
    const done = steps.filter((s) => s.kind === "done").length;
    rows.push({ id: r.id, text: r.text, status: r.status, created_at: r.created_at, steps, done, total: steps.length, pct: steps.length ? Math.round((done / steps.length) * 100) : 0, agents: [...new Set(ts.map((t) => t.agent_id))] });
  }
  return rows.sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
}

export interface FileLike { path: string; name: string; action?: string; version?: number }
export interface MsgLike { id: string; from: string; ts: string; files?: FileLike[] }
export interface ReportLike { id: string; title: string; created_at: string }
export type ReadyItem =
  | { kind: "file"; key: string; ts: string; file: FileLike; from: string }
  | { kind: "report"; key: string; ts: string; id: string; title: string };

/** What is ready: the files agents wrote (the latest version of each path) and the finished reports, newest first. */
export function readyItems(chat: Record<string, MsgLike[]>, reports: Record<string, ReportLike>, limit = 8): ReadyItem[] {
  const files = new Map<string, ReadyItem>();
  for (const list of Object.values(chat)) {
    for (const m of list) {
      if (m.from === "user") continue;
      for (const f of m.files ?? []) {
        if (f.action === "attach") continue;
        const prev = files.get(f.path);
        if (!prev || +new Date(m.ts) >= +new Date(prev.ts)) files.set(f.path, { kind: "file", key: `f:${f.path}`, ts: m.ts, file: f, from: m.from });
      }
    }
  }
  const out: ReadyItem[] = [...files.values()];
  for (const r of Object.values(reports)) out.push({ kind: "report", key: `r:${r.id}`, ts: r.created_at, id: r.id, title: r.title });
  return out.sort((a, b) => +new Date(b.ts) - +new Date(a.ts)).slice(0, limit);
}

/** Up to `n` example requests, one per distinct known role of the real team (its first agent), so they match who is there. */
export function exampleRoles(order: string[], agents: Record<string, { role?: string } | undefined>, known: ReadonlySet<string>, n = 4): { agentId: string; role: string }[] {
  const seen = new Set<string>();
  const out: { agentId: string; role: string }[] = [];
  for (const id of order) {
    const role = agents[id]?.role;
    if (!role || !known.has(role) || seen.has(role)) continue;
    seen.add(role);
    out.push({ agentId: id, role });
    if (out.length >= n) break;
  }
  return out;
}

export type MobileTab = "chat" | "running" | "team";
export const MOBILE_TABS: MobileTab[] = ["chat", "running", "team"];
