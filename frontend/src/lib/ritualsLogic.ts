/**
 * Pure helpers of the rituals UI and of the offline demo (docs/architecture/rituals.md). No React, no network.
 * buildStandupEntries mirrors backend/internal/rituals/build.go (standupEntry) so the mock answers like the server.
 */

export type RitualKind = "standup" | "retro";
export type ItemStatus = "done" | "failed" | "running" | "pending" | "blocked" | "awaiting_approval" | "approval_pending" | "more";
export interface RitualItem { task_id?: string; title: string; status: ItemStatus }
export interface RetroStats { tasks_done: number; tasks_failed: number; rejected: number; reworks: number; score: number | null; level?: string; trend?: string }
export interface RitualEntry {
  agent_id: string; name: string; role: string;
  done: RitualItem[]; next: RitualItem[]; blockers: RitualItem[];
  lines: string[]; quiet: boolean; retro?: RetroStats;
}

/** How long the participants stay in the meeting room after a ritual.held event. */
export const GATHER_MS = 45_000;
const MAX_ITEMS = 3;
const MAX_TITLE = 80;

export interface Gathering { ids: string[]; until: number; kind: RitualKind }

/** The office gathering announced by a ritual.held frame (null when the payload has no participants). */
export function gatheringFrom(payload: any, now: number): Gathering | null {
  const ids = Array.isArray(payload?.agent_ids) ? payload.agent_ids.filter((x: unknown) => typeof x === "string" && x) : [];
  if (!ids.length) return null;
  return { ids: Array.from(new Set<string>(ids)), until: now + GATHER_MS, kind: payload?.kind === "retro" ? "retro" : "standup" };
}

export function isGathering(g: Gathering | null, now: number): boolean {
  return !!g && now < g.until;
}

/** Real number of items of a list ("+N" markers expanded). */
export function realCount(items: RitualItem[]): number {
  let n = 0;
  for (const it of items) n += it.status === "more" ? Number(it.title.replace("+", "")) || 0 : 1;
  return n;
}

export function entryTotals(entries: RitualEntry[]) {
  let done = 0, next = 0, blockers = 0, quiet = 0;
  for (const e of entries) {
    done += realCount(e.done); next += realCount(e.next); blockers += realCount(e.blockers);
    if (e.quiet) quiet++;
  }
  return { done, next, blockers, quiet, present: entries.length };
}

/** Toggles a weekday (0 = Sunday) and keeps the list sorted and unique. */
export function toggleDay(days: number[], d: number): number[] {
  const s = new Set(days);
  if (s.has(d)) s.delete(d); else s.add(d);
  return Array.from(s).sort((a, b) => a - b);
}

function clip(s: string): string {
  const t = s.split(/\s+/).filter(Boolean).join(" ");
  const r = Array.from(t);
  return r.length <= MAX_TITLE ? t : r.slice(0, MAX_TITLE - 1).join("").trim() + "…";
}

function cap(items: RitualItem[]): RitualItem[] {
  if (items.length <= MAX_ITEMS) return items;
  return [...items.slice(0, MAX_ITEMS), { title: `+${items.length - MAX_ITEMS}`, status: "more" }];
}

interface TaskLike { id: string; title: string; agent_id: string; status: string; created_at: string; finished_at: string | null }
interface ApprovalLike { task_id: string; agent_id: string; title: string; status: string; created_at: string }
interface AgentLike { id: string; name: string; role: string }

/** Stand-up entries for the window [from, to): what each agent finished, what it will do, what blocks it. */
export function buildStandupEntries(agents: AgentLike[], tasks: TaskLike[], approvals: ApprovalLike[], from: number, to: number): RitualEntry[] {
  return agents.map((a) => {
    const mine = tasks.filter((t) => t.agent_id === a.id);
    const at = (t: TaskLike) => Date.parse(t.finished_at || t.created_at);
    const asc = [...mine].sort((x, y) => at(x) - at(y));
    const done: RitualItem[] = [];
    for (const t of [...asc].reverse()) {
      const f = t.finished_at ? Date.parse(t.finished_at) : NaN;
      if ((t.status === "done" || t.status === "failed") && f >= from && f < to) done.push({ task_id: t.id, title: clip(t.title), status: t.status as ItemStatus });
    }
    const next: RitualItem[] = [];
    const blockers: RitualItem[] = [];
    for (const t of asc) if (t.status === "running") next.push({ task_id: t.id, title: clip(t.title), status: "running" });
    for (const t of asc) {
      if (t.status === "pending") next.push({ task_id: t.id, title: clip(t.title), status: "pending" });
      else if (t.status === "blocked") blockers.push({ task_id: t.id, title: clip(t.title), status: "blocked" });
      else if (t.status === "awaiting_approval") blockers.push({ task_id: t.id, title: clip(t.title), status: "awaiting_approval" });
    }
    const waiting = new Set(blockers.map((b) => b.task_id));
    const apps = approvals.filter((p) => p.agent_id === a.id && p.status === "pending").sort((x, y) => Date.parse(x.created_at) - Date.parse(y.created_at));
    for (const p of apps) if (!waiting.has(p.task_id)) blockers.push({ task_id: p.task_id, title: clip(p.title), status: "approval_pending" });
    const e: RitualEntry = { agent_id: a.id, name: a.name, role: a.role, done: cap(done), next: cap(next), blockers: cap(blockers), lines: [], quiet: false };
    e.quiet = !e.done.length && !e.next.length && !e.blockers.length;
    return e;
  });
}
