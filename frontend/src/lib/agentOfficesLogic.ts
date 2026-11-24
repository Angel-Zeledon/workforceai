/** Which office an agent belongs to, said the same way on every screen (directory, positions, agent sheet, hire).
 *  One base office per agent (the one `GET /agents/{id}/access` reports) plus optional "also works in" offices
 *  (`GET /agents/{id}/offices`, docs/architecture/08-api.md §57). Pure: tests in agentOfficesLogic.test.mjs. */

export const GENERAL_ID = "general";

export interface OfficeRef { id: string; name: string }
export interface AgentOfficeRow {
  office_id: string; name: string; status: "pending" | "accepted";
  granted_by?: string; accepted_by?: string; can_accept: boolean; can_remove: boolean;
}
/** Response of every `/agents/{id}/offices` route. `enabled` is the server's MULTI_OFFICE_AGENTS switch. */
export interface AgentOfficesView { agent_id: string; base_office: OfficeRef; offices: AgentOfficeRow[]; enabled: boolean }

/** No office, or General, is the General office: nobody is "without an office". */
export const isGeneral = (id?: string | null): boolean => !id || id === GENERAL_ID;

/** The base office of anything that carries `office_id` / `office_name` (directory entry, positions view, chart member). */
export function baseOffice(src: { office_id?: string | null; office_name?: string | null } | null | undefined): OfficeRef {
  const id = src?.office_id ?? "";
  return isGeneral(id) ? { id: GENERAL_ID, name: "" } : { id, name: src?.office_name ?? "" };
}

/** The one text for an office. `generalLabel` is the translated "General". A named office keeps its name. */
export function officeLabel(o: OfficeRef | null | undefined, generalLabel: string): string {
  if (!o || isGeneral(o.id)) return generalLabel;
  return o.name || o.id;
}

export interface OtherOffice { id: string; label: string; pending: boolean; canAccept: boolean; canRemove: boolean }
export interface OfficesSummary { base: OfficeRef; baseLabel: string; others: OtherOffice[]; pending: number; enabled: boolean }

/** Base plus "also works in". With the switch off (or no view) there are no other offices at all: nothing to show or ask for. */
export function officesSummary(base: OfficeRef, view: AgentOfficesView | null | undefined, generalLabel: string): OfficesSummary {
  const enabled = !!view?.enabled;
  const others = enabled
    ? (view?.offices ?? []).filter((r) => r.office_id !== base.id && !(isGeneral(r.office_id) && isGeneral(base.id)))
        .map((r) => ({ id: r.office_id, label: officeLabel({ id: r.office_id, name: r.name }, generalLabel), pending: r.status === "pending", canAccept: r.can_accept, canRemove: r.can_remove }))
    : [];
  return { base, baseLabel: officeLabel(base, generalLabel), others, pending: others.filter((o) => o.pending).length, enabled };
}

/** Offices a person could still be invited to: not the base, not already listed. */
export function invitableOffices<O extends { id: string; can_enter?: boolean }>(all: O[], base: OfficeRef, view: AgentOfficesView | null | undefined): O[] {
  const taken = new Set([base.id, ...(view?.offices ?? []).map((r) => r.office_id)]);
  return all.filter((o) => !taken.has(o.id) && !(isGeneral(o.id) && isGeneral(base.id)) && o.can_enter !== false);
}

export interface AgentRowData { id: string; name: string; title: string; office: OfficeRef; searchText: string }

/** Rows grouped by base office (General first, then by name), a text filter (name, title, office, other offices) and an
 *  office filter ("" = all; matches the base office or an accepted/pending other office). */
export function groupByOffice<E extends { id: string; name: string; title?: string; role?: string; office_id?: string; office_name?: string }>(
  entries: E[], opts: { query?: string; office?: string; generalLabel: string; others?: Record<string, OtherOffice[]> },
): { office: OfficeRef; label: string; items: E[] }[] {
  const q = fold((opts.query ?? "").trim());
  const f = opts.office ?? "";
  const by = new Map<string, { office: OfficeRef; items: E[] }>();
  for (const e of entries) {
    const office = baseOffice(e);
    const others = opts.others?.[e.id] ?? [];
    if (f && office.id !== f && !others.some((o) => o.id === f)) continue;
    if (q) {
      const hay = [e.name, e.title, e.role, officeLabel(office, opts.generalLabel), ...others.map((o) => o.label)].map((s) => fold(s ?? "")).join(" ");
      if (!hay.includes(q)) continue;
    }
    const g = by.get(office.id) ?? { office, items: [] };
    g.items.push(e);
    by.set(office.id, g);
  }
  return [...by.values()]
    .map((g) => ({ office: g.office, label: officeLabel(g.office, opts.generalLabel), items: g.items.sort((a, b) => a.name.localeCompare(b.name)) }))
    .sort((a, b) => (isGeneral(a.office.id) ? 0 : 1) - (isGeneral(b.office.id) ? 0 : 1) || a.label.localeCompare(b.label));
}

/** Offices that appear as a base in the list, for the office filter (General first, then by name). */
export function officeFilterChoices(entries: { office_id?: string; office_name?: string }[], generalLabel: string): { id: string; label: string }[] {
  const seen = new Map<string, OfficeRef>();
  for (const e of entries) { const o = baseOffice(e); if (!seen.has(o.id)) seen.set(o.id, o); }
  return [...seen.values()].map((o) => ({ id: o.id, label: officeLabel(o, generalLabel) }))
    .sort((a, b) => (isGeneral(a.id) ? 0 : 1) - (isGeneral(b.id) ? 0 : 1) || a.label.localeCompare(b.label));
}

export type AskerKind = "onlyOwner" | "office" | "people" | "company";
/** Who may ask this agent for work, as a kind plus the offices that also reach it (accepted ones). */
export function askers(e: { visibility: string; owner_kind?: string; office_id?: string; shares?: unknown[] }, view?: AgentOfficesView | null): { kind: AskerKind; people: number; extra: string[] } {
  const extra = view?.enabled ? view.offices.filter((r) => r.status === "accepted").map((r) => r.name || r.office_id) : [];
  const people = e.shares?.length ?? 0;
  if (e.visibility === "directory" || (isGeneral(e.office_id) && e.owner_kind === "office" && e.visibility === "office")) return { kind: "company", people, extra };
  if (e.visibility === "private") return { kind: "onlyOwner", people, extra };
  if (e.visibility === "people") return { kind: "people", people, extra };
  return { kind: "office", people, extra };
}

export function fold(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}
