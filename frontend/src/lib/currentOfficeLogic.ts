/** The office the person is looking at ("all" = every office they can see), and what belongs to it. Pure: tests in
 *  currentOfficeLogic.test.mjs. Spec: docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §4-§5. */
import { GENERAL_ID, isGeneral } from "./agentOfficesLogic.ts";

export const ALL_OFFICES = "all";

export interface OfficeLite { id: string; name: string; general?: boolean; visibility?: "open" | "private"; can_enter: boolean; is_member?: boolean }
export interface OfficeChoice { id: string; name: string; locked: boolean; general: boolean }

/** Storage key: per person and device. */
export const currentOfficeKey = (userId?: string | null) => `wf.office.current.${userId || "anon"}`;

/** The selector's rows, "Todas mis oficinas" excluded (the UI adds it). With a single office there is no selector (null).
 *  General first, then the ones the person can enter, then closed ones (private, not a member) last. */
export function officeChoices(offices: OfficeLite[] | null | undefined): OfficeChoice[] | null {
  const list = offices ?? [];
  if (list.length <= 1) return null;
  const rank = (o: OfficeLite) => (!o.can_enter ? 2 : o.general || isGeneral(o.id) ? 0 : 1);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
    .map((o) => ({ id: o.id, name: o.name, locked: !o.can_enter, general: !!o.general || isGeneral(o.id) }));
}

/** The office to use given what was stored: it must still exist and be enterable, otherwise "all". With no selector, "all". */
export function resolveCurrent(stored: string | null | undefined, offices: OfficeLite[] | null | undefined): string {
  if (!stored || stored === ALL_OFFICES) return ALL_OFFICES;
  if (!officeChoices(offices)) return ALL_OFFICES;
  const o = (offices ?? []).find((x) => x.id === stored);
  return o && o.can_enter ? o.id : ALL_OFFICES;
}

/** The office an item belongs to: no `office_id` is General. */
export const officeOf = (id?: string | null): string => (isGeneral(id) ? GENERAL_ID : (id as string));

/** Is something of office `itemOffice` in the current scope? "all" lets everything through. */
export function inOffice(current: string, itemOffice?: string | null): boolean {
  return current === ALL_OFFICES || officeOf(itemOffice) === officeOf(current);
}

export function filterByOffice<T>(items: T[], current: string, officeIdOf: (t: T) => string | null | undefined): T[] {
  return current === ALL_OFFICES ? items : items.filter((t) => inOffice(current, officeIdOf(t)));
}

export interface AlsoIn { id: string; pending: boolean }

/** Does the agent belong to the office? Its base, or an accepted "also works in" (a pending invitation is not yet). */
export function agentInOffice(current: string, base: string | null | undefined, alsoIn: AlsoIn[] = []): boolean {
  if (current === ALL_OFFICES) return true;
  if (inOffice(current, base)) return true;
  return alsoIn.some((o) => !o.pending && officeOf(o.id) === officeOf(current));
}

/** The agents of the office, in the order given. */
export function filterAgentIds(order: string[], current: string, baseOf: (id: string) => string | null | undefined, alsoIn: Record<string, AlsoIn[]> = {}): string[] {
  return current === ALL_OFFICES ? order : order.filter((id) => agentInOffice(current, baseOf(id), alsoIn[id]));
}

/** Agents of other offices that work for this one: they sit at a visiting desk in the 3D. None in "all". */
export function visitorIds(order: string[], current: string, baseOf: (id: string) => string | null | undefined, alsoIn: Record<string, AlsoIn[]> = {}): string[] {
  if (current === ALL_OFFICES) return [];
  return order.filter((id) => !inOffice(current, baseOf(id)) && agentInOffice(current, baseOf(id), alsoIn[id]));
}

/** The office a task/approval belongs to, through its request (approval -> task -> request). */
export function officeOfApproval(a: { office_id?: string | null; task_id?: string }, tasks: Record<string, { office_id?: string | null; request_id?: string }>, requests: Record<string, { office_id?: string | null }>): string {
  if (a.office_id) return officeOf(a.office_id);
  const t = a.task_id ? tasks[a.task_id] : undefined;
  if (t?.office_id) return officeOf(t.office_id);
  return officeOf(t?.request_id ? requests[t.request_id]?.office_id : undefined);
}

/** `office_id` a message goes with: the current office, or the one picked in "Para:" while in "all" (General by default). */
export function sendOfficeId(current: string, picked?: string | null): string {
  return current !== ALL_OFFICES ? current : officeOf(picked);
}

/** Visit label of an agent busy elsewhere: "ocupado en Legal", or the generic text when that office is closed to the person. */
export function busyElsewhere(busyOffice: string | null | undefined, current: string, offices: OfficeLite[] | null | undefined): { kind: "none" } | { kind: "named"; name: string } | { kind: "hidden" } {
  if (!busyOffice || inOffice(current, busyOffice)) return { kind: "none" };
  const o = (offices ?? []).find((x) => x.id === officeOf(busyOffice));
  return o && o.can_enter ? { kind: "named", name: o.name } : { kind: "hidden" };
}

/** Free table positions of the 3D layout for visitors: the first free spots out of a candidate list, skipping ones in use. */
export function visitSpots(candidates: { x: number; z: number }[], used: { x: number; z: number }[], n: number, gap = 1.2): { x: number; z: number }[] {
  const out: { x: number; z: number }[] = [];
  for (const c of candidates) {
    if (out.length >= n) break;
    if ([...used, ...out].every((u) => Math.hypot(u.x - c.x, u.z - c.z) >= gap)) out.push(c);
  }
  return out;
}
