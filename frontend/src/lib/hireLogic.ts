/** "Contratar" in three steps: 1 which office, 2 which position (of the office or from the catalog, with its suggested skills),
 *  3 name and who may ask. Pure logic of the wizard (tests: hireLogic.test.mjs). Contract: docs/architecture/08-api.md §57. */

export const HIRE_STEPS = [1, 2, 3] as const;
export type HireStep = (typeof HIRE_STEPS)[number];
export type HireVisibility = "office" | "people" | "directory";

export interface HireDraft {
  office: string;
  /** role template of the catalog (the profession) */
  template: string;
  /** position of the office the agent will fill ("" = none) */
  position: string;
  /** suggested skills the person took out; the rest are sent */
  removed: string[];
  name: string;
  visibility: HireVisibility;
  /** user ids that may ask when visibility is "people" */
  people: string[];
}
export type HireIssue = "office" | "template" | "people" | "name";

export const emptyDraft = (office = ""): HireDraft => ({ office, template: "", position: "", removed: [], name: "", visibility: "office", people: [] });

export const isGeneralOffice = (id: string) => !id || id === "general";
export const NAME_MAX = 40;

/** What is missing to leave a step, or null. Going back never needs anything. */
export function stepIssue(d: HireDraft, step: HireStep): HireIssue | null {
  if (step === 1) return d.office ? null : "office";
  if (step === 2) return d.template ? null : "template";
  if (d.visibility === "people" && d.people.length === 0) return "people";
  if ([...d.name.trim()].length > NAME_MAX) return "name";
  return null;
}

/** The first step with a problem (the one to show when "Contratar" is refused), or null. */
export function firstIssueStep(d: HireDraft): HireStep | null {
  for (const s of HIRE_STEPS) if (stepIssue(d, s)) return s;
  return null;
}

/** Who may ask can be the office, chosen people or the whole company. In General "the office" already is the company. */
export function visibilityChoices(office: string): HireVisibility[] {
  return isGeneralOffice(office) ? ["office", "people"] : ["office", "people", "directory"];
}

/** A change of office resets what depended on it (the position, the removed skills, a visibility the office does not offer). */
export function pickOffice(d: HireDraft, office: string): HireDraft {
  if (d.office === office) return d;
  return { ...d, office, position: "", removed: [], visibility: visibilityChoices(office).includes(d.visibility) ? d.visibility : "office" };
}
export function pickTemplate(d: HireDraft, template: string): HireDraft {
  return d.template === template ? d : { ...d, template, removed: [] };
}
export function pickPosition(d: HireDraft, position: string): HireDraft {
  return d.position === position ? d : { ...d, position, removed: [] };
}

export const toggleRemoved = (removed: string[], id: string): string[] => (removed.includes(id) ? removed.filter((x) => x !== id) : [...removed, id]);
export const keptSkills = <S extends { id: string }>(suggested: S[], d: Pick<HireDraft, "removed">): S[] => suggested.filter((s) => !d.removed.includes(s.id));

export function togglePerson(people: string[], id: string): string[] { return people.includes(id) ? people.filter((x) => x !== id) : [...people, id]; }

/** Body of `POST /agents/from-template`. Without an office nothing is sent: it is General, as always. */
export function hireBody(d: HireDraft, suggested: { id: string }[], locale: string) {
  const skills = keptSkills(suggested, d).map((s) => s.id);
  return {
    template_id: d.template, locale, name: d.name.trim() || undefined,
    office_id: isGeneralOffice(d.office) ? undefined : d.office,
    position_id: d.position && !isGeneralOffice(d.office) ? d.position : undefined,
    skills: skills.length ? skills : undefined,
    visibility: d.visibility,
    shares: d.visibility === "people" ? d.people.map((user_id) => ({ user_id, level: "chat" as const })) : undefined,
  };
}

/** Which step a refused hire belongs to, and the i18n key of its sentence. */
export function hireError(status: number, code: string | undefined, d: HireDraft): { step: HireStep; key: string } {
  if (code === "position_other_office" || code === "other_department") return { step: 2, key: "hire.err.position" };
  if (status === 403) return { step: 1, key: "hire.err.office" };
  if (status === 404) return d.position || d.template ? { step: d.position ? 2 : 1, key: d.position ? "hire.err.position" : "hire.err.office" } : { step: 1, key: "hire.err.office" };
  if (status === 409) return { step: 3, key: "hire.error.conflict" };
  if (status === 400) return { step: 3, key: "hire.err.visibility" };
  return { step: 3, key: "hire.error" };
}
