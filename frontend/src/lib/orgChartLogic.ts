// Pure logic of the organization screens (no React, no path aliases: unit-testable with node --test).
// It mirrors the rules the backend enforces (internal/orgchart) so the editors can guide people before they
// save; the backend stays the authority and its answer is what the screen shows.

export type RefKind = "person" | "agent";
export interface Ref { kind: RefKind | ""; id: string }
export const NO_REF: Ref = { kind: "", id: "" };
export const OWNER_ID = "owner";
export const isNoRef = (r?: Ref | null) => !r || (!r.kind && !r.id);
export const refKey = (r: Ref) => `${r.kind}:${r.id}`;
export const sameRef = (a?: Ref | null, b?: Ref | null) => (isNoRef(a) && isNoRef(b)) || (!!a && !!b && a.kind === b.kind && a.id === b.id);

export interface DeptLite { id: string; name: string; parent_id: string }
export interface DeptNodeLite extends DeptLite { children: DeptNodeLite[] }

export const MAX_DEPTH = 8;
export const MAX_NAME = 80;
export const COST_CENTER_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$/;

/** Walks child -> parent links. Returns the cycle (first node repeated at the end) or null. */
export function detectCycle(parent: Record<string, string>): string[] | null {
  const state: Record<string, number> = {};
  for (const start of Object.keys(parent).sort()) {
    if (state[start]) continue;
    const path: string[] = [];
    let cur = start;
    while (cur) {
      if (state[cur] === 2) { cur = ""; break; }
      if (state[cur] === 1) return [...path.slice(path.indexOf(cur)), cur];
      state[cur] = 1;
      path.push(cur);
      cur = parent[cur] ?? "";
    }
    for (const n of path) state[n] = 2;
  }
  return null;
}

/** The id and the ids of every sub-department below it. */
export function descendantIds(deps: DeptLite[], id: string): Set<string> {
  const out = new Set<string>([id]);
  let grew = true;
  while (grew) {
    grew = false;
    for (const d of deps) if (d.parent_id && out.has(d.parent_id) && !out.has(d.id)) { out.add(d.id); grew = true; }
  }
  return out;
}

export function wouldCycle(deps: DeptLite[], id: string, newParent: string): boolean {
  const parent: Record<string, string> = {};
  for (const d of deps) parent[d.id] = d.parent_id;
  parent[id] = newParent;
  return detectCycle(parent) !== null;
}

export interface FlatDept { id: string; name: string; depth: number; parent_id: string }

/** Departments in tree order (parents before children, siblings by name) with their depth. */
export function flattenDepartments(deps: DeptLite[]): FlatDept[] {
  const byParent = new Map<string, DeptLite[]>();
  const ids = new Set(deps.map((d) => d.id));
  for (const d of deps) {
    const p = d.parent_id && ids.has(d.parent_id) ? d.parent_id : "";
    byParent.set(p, [...(byParent.get(p) ?? []), d]);
  }
  const out: FlatDept[] = [];
  const seen = new Set<string>();
  const walk = (p: string, depth: number) => {
    for (const d of [...(byParent.get(p) ?? [])].sort((a, b) => a.name.localeCompare(b.name))) {
      if (seen.has(d.id)) continue;
      seen.add(d.id);
      out.push({ id: d.id, name: d.name, depth, parent_id: d.parent_id });
      walk(d.id, depth + 1);
    }
  };
  walk("", 0);
  return out;
}

/** Choices for a "parent department" picker: never the department itself nor anything below it (that would be a cycle),
 *  and never a parent that would push a subtree past the depth limit. */
export function parentOptions(deps: DeptLite[], editingId?: string): FlatDept[] {
  const banned = editingId ? descendantIds(deps, editingId) : new Set<string>();
  const flat = flattenDepartments(deps).filter((d) => !banned.has(d.id));
  if (!editingId) return flat.filter((d) => d.depth + 1 < MAX_DEPTH);
  const heightBelow = subtreeHeight(deps, editingId);
  return flat.filter((d) => d.depth + 1 + heightBelow <= MAX_DEPTH);
}

/** Levels of a department's subtree, itself included (a leaf is 1). */
export function subtreeHeight(deps: DeptLite[], id: string): number {
  const kids = (x: string) => deps.filter((d) => d.parent_id === x);
  const h = (x: string, guard: number): number => (guard > MAX_DEPTH + 2 ? 0 : 1 + Math.max(0, ...kids(x).map((k) => h(k.id, guard + 1))));
  return h(id, 0);
}

export type DeptIssue = "name" | "name_long" | "cost_center" | "parent_cycle" | "name_taken";

export function validateDepartment(d: { name: string; parent_id: string; cost_center: string }, deps: DeptLite[], editingId?: string): DeptIssue | null {
  const name = d.name.trim().replace(/\s+/g, " ");
  if (!name) return "name";
  if ([...name].length > MAX_NAME) return "name_long";
  if (d.cost_center && !COST_CENTER_RE.test(d.cost_center)) return "cost_center";
  if (d.parent_id && editingId && wouldCycle(deps, editingId, d.parent_id)) return "parent_cycle";
  if (deps.some((x) => x.id !== editingId && x.name.trim().toLowerCase() === name.toLowerCase())) return "name_taken";
  return null;
}

// ---- schedules ----

export interface ScheduleLite { enabled: boolean; timezone: string; days: number[]; start: string; end: string }
export const WORKDAYS = [1, 2, 3, 4, 5];
export const ALL_DAYS = [0, 1, 2, 3, 4, 5, 6];

export function defaultSchedule(timezone = "UTC"): ScheduleLite {
  return { enabled: false, timezone, days: [...WORKDAYS], start: "09:00", end: "18:00" };
}

const CLOCK = /^(\d{2}):(\d{2})$/;
/** Minutes after midnight of "HH:MM" ("24:00" allowed), or null. */
export function parseClock(v: string): number | null {
  const m = CLOCK.exec(v.trim());
  if (!m) return null;
  const h = Number(m[1]), mi = Number(m[2]);
  if (mi > 59 || h > 24 || (h === 24 && mi !== 0)) return null;
  return h * 60 + mi;
}

export function validTimezone(tz: string): boolean {
  if (!tz.trim()) return false;
  try { new Intl.DateTimeFormat("en-US", { timeZone: tz }); return true; } catch { return false; }
}

export type ScheduleIssue = "timezone" | "days" | "clock" | "same_time";

export function validateSchedule(s: ScheduleLite): ScheduleIssue | null {
  if (!s.enabled) return null;
  if (!validTimezone(s.timezone)) return "timezone";
  if (s.days.length === 0 || new Set(s.days).size !== s.days.length || s.days.some((d) => !Number.isInteger(d) || d < 0 || d > 6)) return "days";
  const a = parseClock(s.start), b = parseClock(s.end);
  if (a === null || b === null || a === 24 * 60) return "clock";
  if (a === b) return "same_time";
  return null;
}

export const toggleDay = (days: number[], d: number) => (days.includes(d) ? days.filter((x) => x !== d) : [...days, d].sort((a, b) => a - b));

const WD: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

/** Wall clock (weekday, minutes after midnight) of an instant in a timezone, via Intl (DST aware). */
export function wallClock(at: Date, timezone: string): { day: number; minutes: number } {
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: timezone, weekday: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(at);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  return { day: WD[get("weekday")] ?? 0, minutes: (Number(get("hour")) % 24) * 60 + Number(get("minute")) };
}

/** Same rule as the backend: wall clock of the schedule's timezone; an overnight window belongs to the day it starts;
 *  a disabled or invalid schedule is always open. */
export function isOpenAt(s: ScheduleLite, at: Date): boolean {
  if (!s.enabled || validateSchedule(s) !== null) return true;
  const st = parseClock(s.start)!, en = parseClock(s.end)!;
  const { day, minutes } = wallClock(at, s.timezone);
  const yesterday = (day + 6) % 7;
  if (st < en) return s.days.includes(day) && minutes >= st && minutes < en;
  return (s.days.includes(day) && minutes >= st) || (s.days.includes(yesterday) && minutes < en);
}

// ---- tools ----

/** A job description can only narrow the tools of the role: keep what is allowed, in the role's order. */
export function narrowTools(roleTools: string[], selected: string[]): string[] {
  return roleTools.filter((t) => selected.includes(t));
}
export const toggleTool = (selected: string[], t: string) => (selected.includes(t) ? selected.filter((x) => x !== t) : [...selected, t]);

// ---- reporting lines ----

/** Everyone who would create a cycle if chosen as the boss of `who` (so the picker can hide them). */
export function bossCandidates(edges: Record<string, string>, who: string, all: string[]): string[] {
  return all.filter((c) => c !== who && !detectCycle({ ...edges, [who]: c }));
}

// ---- budgets ----

export function parseBudget(v: string): { value: number | null; ok: boolean } {
  const s = v.trim().replace(",", ".");
  if (s === "") return { value: null, ok: true };
  const n = Number(s);
  return Number.isFinite(n) && n >= 0 && n <= 1_000_000 ? { value: Math.round(n * 100) / 100, ok: true } : { value: null, ok: false };
}

// ---- delegation ----

export type SelectorType = "any" | "any_person" | "any_agent" | "person" | "agent" | "department";
export const SELECTOR_NEEDS_ID: SelectorType[] = ["person", "agent", "department"];
export interface SelectorLite { type: SelectorType; id: string }

/** The same "most specific wins" scale as the backend, to preview which rule a pair would hit. */
export const specificity = (s: SelectorLite) => (s.type === "person" || s.type === "agent" ? 3 : s.type === "department" ? 2 : s.type === "any" ? 0 : 1);

export function ruleIssue(r: { from: SelectorLite; to: SelectorLite; effect: string; require_approval: boolean }): "from" | "to" | "deny_approval" | null {
  for (const [k, s] of [["from", r.from], ["to", r.to]] as const) if (SELECTOR_NEEDS_ID.includes(s.type) && !s.id) return k;
  if (r.effect === "deny" && r.require_approval) return "deny_approval";
  return null;
}
