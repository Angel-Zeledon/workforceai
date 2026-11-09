/**
 * "Agentes y oficinas" (/org) in 4 tabs (proposal 5.2 #3 and row 3.1, with the owner's decision: "oficina" is the
 * word for the group): Agentes · Oficinas · Organigrama · Reglas de delegación.
 *  - Agentes holds the directory, "Contratar agente" and the agents' job descriptions (Puestos), as a section inside.
 *  - Oficinas holds the offices with their own space (3D place, members, memory) and the ones without a space yet
 *    (the org chart's departments: the data and the backend do not change, only how they are shown).
 *
 * Pure (no imports): the screen uses it and the unit test runs it with `node --test`.
 */

export const ORG_TABS = ["agents", "offices", "chart", "delegation"] as const;
export type OrgTab = (typeof ORG_TABS)[number];

/** The sections inside Agentes (the old "directory" and "roles" tabs). */
export const AGENT_SECTIONS = ["directory", "roles"] as const;
export type AgentSection = (typeof AGENT_SECTIONS)[number];

/** The 6 tabs the page had before; they still arrive in old links (`/org?tab=departments`). */
export const LEGACY_ORG_TABS = ["chart", "offices", "directory", "departments", "roles", "delegation"] as const;

/** Where a link lands: the tab, the section inside Agentes, or the "without a space yet" part of Oficinas. */
export interface OrgPlace { tab: OrgTab; section?: AgentSection; focus?: "departments" }

const isIn = <T extends string>(list: readonly T[], v: string): v is T => (list as readonly string[]).includes(v);

/**
 * Translates a tab name (new or old) to the place of the new page. Unknown or empty names give null (the page keeps
 * what it shows, Agentes the first time).
 */
export function resolveOrgTab(raw: string | null | undefined): OrgPlace | null {
  const k = String(raw ?? "").trim().toLowerCase();
  if (!k) return null;
  if (k === "directory") return { tab: "agents", section: "directory" };
  if (k === "roles") return { tab: "agents", section: "roles" };
  if (k === "departments") return { tab: "offices", focus: "departments" };
  if (isIn(ORG_TABS, k)) return { tab: k };
  return null;
}

/**
 * Splits the org chart's departments into the ones that already have their own space (an office with the same id)
 * and the ones that do not yet, keeping the order they come in (parents first).
 */
export function splitBySpace<D extends { id: string }>(depts: readonly D[], offices: readonly { id: string; general?: boolean }[]): { withSpace: D[]; withoutSpace: D[] } {
  const spaced = new Set(offices.filter((o) => !o.general).map((o) => o.id));
  return { withSpace: depts.filter((d) => spaced.has(d.id)), withoutSpace: depts.filter((d) => !spaced.has(d.id)) };
}
