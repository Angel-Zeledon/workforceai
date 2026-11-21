/** Pure helpers of company goals (F1). No React, no network. */
import type { Goal, GoalInput, GoalStatus } from "./goalsApi";

export const GOAL_STATUSES: GoalStatus[] = ["active", "achieved", "archived"];
export const GOAL_LIMITS = { title: 120, description: 280, period: 40 } as const;

/** An unknown status from a newer server is shown as active rather than breaking the view. */
export function normalizeGoalStatus(s: string | null | undefined): GoalStatus {
  return (GOAL_STATUSES as string[]).includes(String(s)) ? (s as GoalStatus) : "active";
}

/** i18n key of the status label. */
export function goalStatusKey(s: string | null | undefined): string {
  return `goals.status.${normalizeGoalStatus(s)}`;
}

const rank = (s: string) => GOAL_STATUSES.indexOf(normalizeGoalStatus(s));

/** Active first (soonest deadline first, goals without a deadline last), then achieved, then archived (newest change first). */
export function sortGoals<T extends Pick<Goal, "status" | "title" | "due_date" | "updated_at">>(goals: T[]): T[] {
  return [...goals].sort((a, b) => {
    const r = rank(a.status) - rank(b.status);
    if (r) return r;
    if (normalizeGoalStatus(a.status) === "active") {
      const da = a.due_date || "9999-99-99", db = b.due_date || "9999-99-99";
      if (da !== db) return da < db ? -1 : 1;
      return a.title.localeCompare(b.title, "es", { sensitivity: "base" });
    }
    return (b.updated_at || "").localeCompare(a.updated_at || "");
  });
}

const len = (s: string) => Array.from(s).length;

export type GoalIssue = "title_required" | "title_long" | "description_long" | "period_long" | "due_invalid";

/** Client-side check before sending (the server has the last word). Returns the first problem or null. */
export function validateGoal(v: { title: string; description?: string; period?: string; due_date?: string }): GoalIssue | null {
  const title = v.title.trim();
  if (!title) return "title_required";
  if (len(title) > GOAL_LIMITS.title) return "title_long";
  if (len((v.description ?? "").trim()) > GOAL_LIMITS.description) return "description_long";
  if (len((v.period ?? "").trim()) > GOAL_LIMITS.period) return "period_long";
  if (v.due_date && !parseDay(v.due_date)) return "due_invalid";
  return null;
}

function parseDay(s: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return null;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return d.getFullYear() === Number(m[1]) && d.getMonth() === Number(m[2]) - 1 && d.getDate() === Number(m[3]) ? d : null;
}

/** "YYYY-MM-DD" as a readable date in the locale ("" when absent or invalid). */
export function formatDueDate(due: string | null | undefined, locale = "es"): string {
  const d = due ? parseDay(due) : null;
  if (!d) return "";
  return new Intl.DateTimeFormat(locale, { day: "numeric", month: "short", year: "numeric" }).format(d);
}

/** An active goal whose deadline has passed. */
export function isOverdue(g: Pick<Goal, "status" | "due_date">, now: Date = new Date()): boolean {
  const d = g.due_date ? parseDay(g.due_date) : null;
  if (!d || normalizeGoalStatus(g.status) !== "active") return false;
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  return d.getTime() < today.getTime();
}

/** Period and readable deadline, each "" when missing. */
export function goalWhen(g: Pick<Goal, "period" | "due_date">, locale = "es"): { period: string; due: string } {
  return { period: (g.period ?? "").trim(), due: formatDueDate(g.due_date, locale) };
}

/** Body of POST /goals: optional fields are left out when empty. */
export function goalCreateBody(v: { title: string; description: string; period: string; due_date: string; office_id: string }): GoalInput {
  const body: GoalInput = { title: v.title.trim() };
  if (v.description.trim()) body.description = v.description.trim();
  if (v.period.trim()) body.period = v.period.trim();
  if (v.due_date) body.due_date = v.due_date;
  if (v.office_id) body.office_id = v.office_id;
  return body;
}

/** Goals a request, project or message can point to: only the active ones. */
export function linkableGoals<T extends Pick<Goal, "status" | "title" | "due_date" | "updated_at">>(goals: T[]): T[] {
  return sortGoals(goals).filter((g) => normalizeGoalStatus(g.status) === "active");
}

export function activeCount(goals: Pick<Goal, "status">[]): number {
  return goals.filter((g) => normalizeGoalStatus(g.status) === "active").length;
}

export type GoalErrorKind = "forbidden" | "limit" | "invalid" | "notfound" | "generic";

/** Maps a failed call ("METHOD path -> 403") to a kind the screen translates. */
export function goalErrorKind(e: unknown): GoalErrorKind {
  const raw = (e as { status?: unknown })?.status;
  const status = typeof raw === "number" ? raw : Number(/-> (\d{3})\b/.exec(String((e as Error)?.message ?? ""))?.[1] ?? 0);
  if (status === 403) return "forbidden";
  if (status === 409) return "limit";
  if (status === 400) return "invalid";
  if (status === 404) return "notfound";
  return "generic";
}
