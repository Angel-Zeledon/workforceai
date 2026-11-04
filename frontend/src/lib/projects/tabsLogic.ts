/**
 * The project detail in 4 tabs (docs/superpowers/plans/2026-11-04-ux-tanda-2.md, task A; proposal 5.3):
 * Seguimiento · Plan · Costos · Permisos. Plan opens the map and keeps the other ways of looking at the plan behind
 * "Otras vistas" (board, by agent, plan calendar, who passed what, health, history of changes).
 *
 * Pure (no imports): the store and the screen use it, and the unit test runs it with `node --test`.
 * Permisos keeps the key "approvals" so the old `pv-tab-approvals` test id and `?tab=approvals` links still work.
 */

export const PROJECT_TABS = ["follow", "plan", "costs", "approvals"] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];

/** The views inside Plan, in the order of the selector; the map first (it is what Plan opens with). */
export const PLAN_VIEWS = ["map", "kanban", "lanes", "timeline", "delegation", "health", "changes"] as const;
export type PlanView = (typeof PLAN_VIEWS)[number];
export const DEFAULT_PLAN_VIEW: PlanView = "map";

/** The 10 tabs the detail had before (they still arrive in old links, `?tab=kanban`, and in calls like setTab("map")). */
export const LEGACY_PROJECT_TABS = ["follow", "health", "map", "timeline", "kanban", "lanes", "delegation", "approvals", "changes", "costs"] as const;
export type LegacyProjectTab = (typeof LEGACY_PROJECT_TABS)[number];

/** Anything that names a place in the detail: a new tab, an old tab, a view inside Plan, or "permissions". */
export type ProjectTabLink = ProjectTab | LegacyProjectTab | PlanView | "permissions";

export interface ProjectPlace { tab: ProjectTab; planView?: PlanView }

const isIn = <T extends string>(list: readonly T[], v: string): v is T => (list as readonly string[]).includes(v);

/**
 * Translates a tab name (new or old, from a link or from code) to the tab of the new detail and, for the views that
 * now live inside Plan, the view to open there. Unknown or empty names give null (the screen keeps what it shows).
 * `?tab=plan` gives no view: Plan shows the one already chosen (the map the first time).
 */
export function resolveProjectTab(raw: string | null | undefined): ProjectPlace | null {
  const k = String(raw ?? "").trim().toLowerCase();
  if (!k) return null;
  if (k === "permissions") return { tab: "approvals" };
  if (isIn(PROJECT_TABS, k)) return { tab: k };
  if (isIn(PLAN_VIEWS, k)) return { tab: "plan", planView: k };
  return null;
}
