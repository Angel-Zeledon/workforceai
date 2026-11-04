/**
 * Simple mode / see everything (docs/superpowers/plans/2026-11-03-ux-tanda-1.md, decision 3 of the owner).
 * Nothing is removed: simple mode only decides what is OPEN when a screen loads; the rest is one click away behind
 * "Ver todo". Pure (no React): the hook that reads the person's choice lives in lib/viewMode.ts.
 *
 * Who sees what by default: the server says it (GET /org/settings -> view_mode_default): "simple" for organizations
 * created on or after the release of this change, "full" for the ones that already existed and for the demo. An
 * older server that does not send it means "full", so nothing changes for anyone until the backend says so.
 * Once the person flips the switch, their choice wins and is remembered (per person, on this device).
 */

export type ViewModeDefault = "simple" | "full";

/** The stored choice ("1" = see everything, "0" = simple) wins; without one, the server default; without that, full. */
export function resolveViewAll(stored: string | null | undefined, serverDefault: string | null | undefined): boolean {
  if (stored === "1") return true;
  if (stored === "0") return false;
  return serverDefault !== "simple";
}

/** Storage key of the person's choice. */
export const viewAllKey = (userId: string | null | undefined) => `wf.viewAll.${userId || "anon"}`;

export interface Split<T extends string> { shown: T[]; folded: T[] }

/**
 * Splits a screen's tabs into the ones shown and the ones folded behind "Ver todo". With everything on, all are shown.
 * In simple mode the `simple` ones are shown, plus any in `keep` (the open tab from a deep link, a tab with something
 * waiting for the person), always in the screen's own order.
 */
export function splitTabs<T extends string>(all: readonly T[], simple: readonly T[], viewAll: boolean, keep: readonly (T | null | undefined)[] = []): Split<T> {
  if (viewAll) return { shown: [...all], folded: [] };
  const on = new Set<T>([...simple, ...(keep.filter(Boolean) as T[])]);
  return { shown: all.filter((t) => on.has(t)), folded: all.filter((t) => !on.has(t)) };
}

/** What simple mode keeps open on each screen (the proposal's 5.5). */
// Project (4 tabs, tanda 2): Seguimiento and Permisos ("approvals") open; Plan and Costos behind "Más vistas".
export const SIMPLE_PROJECT_TABS = ["follow", "approvals"] as const;
// Agent panel (5 tabs, tanda 2): Chat and Trabajo open; Qué puede hacer, Qué sabe and Perfil behind "Más".
export const SIMPLE_AGENT_TABS = ["chat", "work"] as const;
export const SIMPLE_DASHBOARD = ["overview", "tasks", "agents"] as const;
