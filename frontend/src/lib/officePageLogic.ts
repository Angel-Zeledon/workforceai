/** The page of one office (`/offices/<id>`): its six tabs and what each lists. Pure: tests in officePageLogic.test.mjs. */

export const OFFICE_PAGE_TABS = ["people", "agents", "positions", "skills", "files", "memory"] as const;
export type OfficePageTab = (typeof OFFICE_PAGE_TABS)[number];

const ALIAS: Record<string, OfficePageTab> = { personas: "people", members: "people", agentes: "agents", puestos: "positions", habilidades: "skills", archivos: "files", drive: "files", memoria: "memory" };

export function resolveOfficeTab(raw: string | null | undefined): OfficePageTab {
  const k = String(raw ?? "").trim().toLowerCase();
  if ((OFFICE_PAGE_TABS as readonly string[]).includes(k)) return k as OfficePageTab;
  return ALIAS[k] ?? "people";
}

/** Agents of an office: the ones whose base it is, and the ones that also work in it (`visiting`, pending ones marked). */
export function officeAgents<E extends { id: string; name: string }>(
  entries: E[], officeId: string, baseOf: (e: E) => string, alsoIn: Record<string, { id: string; pending: boolean }[]> = {},
): { base: E[]; visiting: { entry: E; pending: boolean }[] } {
  const by = (a: E, b: E) => a.name.localeCompare(b.name);
  const base = entries.filter((e) => baseOf(e) === officeId).sort(by);
  const visiting = entries.filter((e) => !base.includes(e)).flatMap((entry) => {
    const o = (alsoIn[entry.id] ?? []).find((x) => x.id === officeId);
    return o ? [{ entry, pending: o.pending }] : [];
  }).sort((a, b) => by(a.entry, b.entry));
  return { base, visiting };
}

/** Skills of an office (`office_id` = it) apart from the company-wide ones it can also use. */
export function skillsOfOffice<S extends { office_id?: string }>(skills: S[], officeId: string): { own: S[]; company: S[] } {
  return { own: skills.filter((s) => !!s.office_id && s.office_id === officeId), company: skills.filter((s) => !s.office_id || s.office_id === "general") };
}
