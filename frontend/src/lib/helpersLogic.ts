/**
 * Pure logic of temporary helpers (no React, no path aliases: unit-testable with node --test).
 * A helper is a part of a request that its owner (an employee) coordinates; it is never a person of the team.
 */

export interface PartLike { index: number; label: string; task_id?: string; status?: string }
export interface GroupLike {
  id: string; request_id: string; owner_agent_id: string; count: number; max_count: number; parallel: number;
  parts: PartLike[]; estimate: { min_usd: number; max_usd: number }; cap_usd: number; spent_usd: number; status: string;
}
export interface TaskWithHelper { helper?: { group_id: string; index?: number } | null }

export type PartKind = "done" | "active" | "queued" | "failed" | "stopped";

/** A task that belongs to a helper group: it stays out of Equipo, Agentes and the plain task lists. */
export const isHelperTask = (t: TaskWithHelper | null | undefined): boolean => !!t?.helper?.index; // index 0 is the responsible's own joining task
export const withoutHelperTasks = <T extends TaskWithHelper>(ts: T[]): T[] => ts.filter((t) => !isHelperTask(t));

/** A proposal waits for the person: nothing is launched until they accept. */
export const isProposal = (g: Pick<GroupLike, "status"> | null | undefined): boolean => g?.status === "proposed";
export const isActiveGroup = (g: Pick<GroupLike, "status"> | null | undefined): boolean => g?.status === "running" || g?.status === "joining";

/** The groups waiting for an answer. */
export const proposals = <G extends GroupLike>(groups: Record<string, G>): G[] => Object.values(groups).filter(isProposal);

/** Count limits: at least 2 helpers (one is just the employee), at most what the plan allows. */
export function clampCount(n: number, g: Pick<GroupLike, "max_count">, minCount = 2): number {
  const hi = Math.max(minCount, Math.floor(g.max_count) || minCount);
  const v = Number.isFinite(n) ? Math.round(n) : minCount;
  return Math.min(hi, Math.max(minCount, v));
}

/** The choices of the quantity selector: a short ladder up to the maximum, the proposed count always included. */
export function countOptions(g: Pick<GroupLike, "count" | "max_count">): number[] {
  const max = Math.max(2, g.max_count);
  const set = new Set<number>([2, clampCount(g.count, g), max]);
  const step = max > 12 ? 4 : 2;
  for (let n = 4; n < max; n += step) set.add(n);
  return [...set].sort((a, b) => a - b);
}

const round2 = (n: number) => Math.round(n * 100) / 100;

/** Estimate and cap for another number of helpers, in proportion to the proposed one. */
export function rescale(g: Pick<GroupLike, "count" | "estimate" | "cap_usd">, count: number): { estimate: { min_usd: number; max_usd: number }; cap_usd: number } {
  const k = g.count > 0 ? count / g.count : 1;
  return { estimate: { min_usd: round2(g.estimate.min_usd * k), max_usd: round2(g.estimate.max_usd * k) }, cap_usd: round2(g.cap_usd * k) };
}

/** How many items each helper takes ("40 contratos, 5 por ayudante"): the biggest share, rounded up. */
export const perHelper = (items: number, helpers: number): number => (helpers > 0 ? Math.ceil(items / helpers) : items);

export function partKind(status: string | undefined, groupStatus?: string): PartKind {
  if (status === "done") return "done";
  if (status === "failed") return "failed";
  if (status === "stopped" || (groupStatus === "stopped" && status !== "running")) return "stopped";
  if (status === "running") return "active";
  return "queued";
}

export interface Counts { done: number; active: number; queued: number; failed: number; stopped: number; total: number }
export function countParts(g: Pick<GroupLike, "parts" | "status">): Counts {
  const c: Counts = { done: 0, active: 0, queued: 0, failed: 0, stopped: 0, total: g.parts.length };
  for (const p of g.parts) c[partKind(p.status, g.status)]++;
  return c;
}

/** Spend against the cap: a 0..100 percentage and whether the cap is reached (the parts left were stopped). */
export function spendOf(g: Pick<GroupLike, "spent_usd" | "cap_usd">): { pct: number; reached: boolean } {
  const pct = g.cap_usd > 0 ? Math.min(100, Math.round((g.spent_usd / g.cap_usd) * 100)) : 0;
  return { pct, reached: g.cap_usd > 0 && g.spent_usd >= g.cap_usd };
}

/**
 * The parts to list. With few parts all are shown in order; with many (50) the ones that need attention come first
 * (failed, then running, then queued) and the rest folds into a count, unless the person expands it.
 */
export function visibleParts<P extends PartLike>(g: { parts: P[]; status: string }, expanded: boolean, limit = 12): { shown: P[]; hidden: number } {
  if (expanded || g.parts.length <= limit) return { shown: g.parts, hidden: 0 };
  const rank = (p: P) => { const k = partKind(p.status, g.status); return k === "failed" ? 0 : k === "active" ? 1 : k === "queued" ? 2 : k === "stopped" ? 3 : 4; };
  const shown = [...g.parts].sort((a, b) => rank(a) - rank(b) || a.index - b.index).slice(0, limit);
  return { shown, hidden: g.parts.length - shown.length };
}

/** "6 de 8": how many parts are done out of all of them. */
export const doneOf = (g: Pick<GroupLike, "parts" | "status">): { done: number; total: number } => ({ done: countParts(g).done, total: g.parts.length });

/** The group that applies to a request (the last one that was not declined). */
export function groupOfRequest<G extends GroupLike>(groups: Record<string, G>, requestId: string): G | undefined {
  const list = Object.values(groups).filter((g) => g.request_id === requestId && g.status !== "declined");
  return list[list.length - 1];
}

/** Merges an incoming group over the known one: a late "proposed" event never undoes a launch. */
export function mergeGroup<G extends GroupLike>(prev: G | undefined, next: G): G {
  if (!prev) return next;
  if (prev.status !== "proposed" && next.status === "proposed") return { ...prev, ...next, status: prev.status };
  return { ...prev, ...next };
}

const BULLET = /^\s*(?:[-*•]|\d+[.)])\s+/;
/** The independent things in a request (demo): a list of 4 or more bullets or "a, b, c, d" after a colon; attachments count too. */
export function splitItems(text: string, attachments = 0): string[] {
  const lines = text.split(/\r?\n/);
  const bullets = lines.filter((l) => BULLET.test(l));
  if (bullets.length >= 4) return bullets.map((l) => l.replace(BULLET, "").trim());
  const after = text.includes(":") ? text.slice(text.indexOf(":") + 1) : "";
  const items = after.split(/\s*(?:,|;|\sy\s|\sand\s|\se\s)\s*/).map((s) => s.trim().replace(/[.?!]+$/, "")).filter(Boolean);
  if (items.length >= 4) return items;
  if (attachments >= 2) return Array.from({ length: attachments }, (_, i) => String(i + 1));
  return [];
}

/** Demo cost of a split: a small range that grows with the parts. */
export function demoEstimate(count: number): { estimate: { min_usd: number; max_usd: number }; cap_usd: number } {
  const min = round2(count * 0.01), max = round2(count * 0.025);
  return { estimate: { min_usd: min, max_usd: max }, cap_usd: max };
}

/** Plan limits of the design (Free 3/6, Pro 6/20, Max 8/50): how many at once and how many parts. */
export const PLAN_LIMITS = { free: { parallel: 3, parts: 6 }, pro: { parallel: 6, parts: 20 }, max: { parallel: 8, parts: 50 } } as const;
