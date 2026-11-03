/** E2 "Oficinas": pure logic of offices, agent access and team memory (tests: officesLogic.test.mjs).
 *  Contract: backend/internal/api/offices.go, offices_agents.go and offices_notes.go. */

export type OfficeVisibility = "open" | "private";
export type AgentVisibility = "private" | "office" | "people" | "directory";
export type ShareLevel = "chat" | "edit";
export type NoteScope = "office" | "person" | "company";

export interface PersonRef { user_id: string; name: string }
export interface OfficeView {
  id: string; name: string; visibility: OfficeVisibility; general: boolean;
  leads: PersonRef[]; members: PersonRef[]; member_count: number; agents: string[];
  can_enter: boolean; can_manage: boolean; is_member: boolean; is_lead: boolean;
}
export interface OfficesResponse {
  offices: OfficeView[]; departments: { id: string; name: string; office: boolean }[]; restricted: boolean;
  me: { user_id: string; admin: boolean; offices: string[] };
}
export interface Rights { see: boolean; use: boolean; edit: boolean }
export interface Share { user_id: string; name?: string; level: ShareLevel }
export interface AccessView {
  owner_kind: "person" | "office"; owner_id: string; owner_name: string; office_id: string; office_name: string;
  visibility: AgentVisibility; shares: Share[]; explicit: boolean; departed?: boolean; previous_owner_name?: string; rights: Rights;
}
export interface DirectoryEntry extends AccessView { id: string; name: string; role: string; title: string }
export interface AccessInput { owner_kind?: "person" | "office"; owner_id?: string; visibility?: AgentVisibility; shares?: Share[] }
export interface TeamNote { id: string; scope: NoteScope; owner_ref: string; text: string; promoted_from?: string; created_by: string; created_at: string; author_name?: string; can_delete?: boolean; promoted?: boolean }

export const VISIBILITIES: readonly AgentVisibility[] = ["private", "office", "people", "directory"];
export const NOTE_MAX = 1000;

/** The badge an office shows: General, private (closed door) or open. */
export function officeBadge(o: Pick<OfficeView, "general" | "visibility">): "general" | "private" | "open" {
  if (o.general) return "general";
  return o.visibility === "private" ? "private" : "open";
}

/** General first, then my offices, then the others, each group by name. */
export function sortOffices(list: OfficeView[]): OfficeView[] {
  const rank = (o: OfficeView) => (o.general ? 0 : o.is_member ? 1 : 2);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

/** Departments an admin can still turn into an office. */
export function convertibleDepartments(r: Pick<OfficesResponse, "departments">): { id: string; name: string }[] {
  return r.departments.filter((d) => !d.office).map(({ id, name }) => ({ id, name }));
}

/** People of the organization not yet members of the office (for "add member"). */
export function addableMembers(people: PersonRef[], o: Pick<OfficeView, "members">): PersonRef[] {
  const inside = new Set(o.members.map((m) => m.user_id));
  return people.filter((p) => !inside.has(p.user_id));
}

export type DirectoryGroup = "mine" | "office" | "shared" | "company" | "visible";
export const DIRECTORY_GROUPS: readonly DirectoryGroup[] = ["mine", "office", "shared", "company", "visible"];

/** Where an agent goes in the directory for me: my agents, shared with me, my offices', the whole company's (General or
 *  the directory), and other offices' (an admin may use them; the others only look). */
export function directoryGroup(e: DirectoryEntry, me: { user_id: string; offices: string[] }): DirectoryGroup {
  if (e.owner_kind === "person" && e.owner_id === me.user_id) return "mine";
  if (e.rights.use && (e.shares.some((s) => s.user_id === me.user_id) || e.visibility === "people" && e.owner_kind === "person")) return "shared";
  if (e.visibility === "directory" || e.office_id === "general" && e.visibility !== "private" && e.owner_kind === "office") return e.rights.use ? "company" : "visible";
  if (me.offices.includes(e.office_id) && e.rights.use) return "office";
  return "visible";
}

/** The visibility label key of an agent: an office agent of General is for the whole company. */
export function visibilityKey(e: Pick<DirectoryEntry, "office_id" | "visibility" | "owner_kind">): string {
  if (e.office_id === "general" && e.owner_kind === "office" && e.visibility === "office") return "offices.badge.general";
  return `offices.vis.${e.visibility}`;
}

/** The directory grouped, in the order of DIRECTORY_GROUPS, without empty groups; a text filter matches name, title, role, owner and office. */
export function directoryGroups(entries: DirectoryEntry[], me: { user_id: string; offices: string[] }, query = ""): { group: DirectoryGroup; items: DirectoryEntry[] }[] {
  const q = fold(query.trim());
  const hit = (e: DirectoryEntry) => !q || [e.name, e.title, e.role, e.owner_name, e.office_name].some((s) => fold(s ?? "").includes(q));
  const by = new Map<DirectoryGroup, DirectoryEntry[]>();
  for (const e of entries.filter(hit)) {
    const g = directoryGroup(e, me);
    by.set(g, [...(by.get(g) ?? []), e]);
  }
  return DIRECTORY_GROUPS.filter((g) => by.has(g)).map((g) => ({ group: g, items: by.get(g)!.sort((a, b) => a.name.localeCompare(b.name)) }));
}

export function fold(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

/** What is wrong with an access change, or null. */
export function accessIssue(i: AccessInput): "owner" | "visibility" | "noShares" | null {
  if (i.owner_kind && !i.owner_id) return "owner";
  if (i.visibility && !VISIBILITIES.includes(i.visibility)) return "visibility";
  if (i.visibility === "people" && (!i.shares || i.shares.length === 0) && i.owner_kind === "person") return "noShares";
  return null;
}

/** Adds or changes one share, keeping one per person. */
export function upsertShare(list: Share[], s: Share): Share[] {
  const out = list.filter((x) => x.user_id !== s.user_id);
  out.push(s);
  return out;
}

/** What is wrong with a note text, or null. */
export function noteIssue(text: string): "empty" | "long" | null {
  const t = text.trim();
  if (!t) return "empty";
  if ([...t].length > NOTE_MAX) return "long";
  return null;
}

/** The offices a person can pick in the 3D view: the ones they can enter; private ones they are not in are listed locked.
 *  Null when there is only General (no picker). */
export function layoutChoices(list: OfficeView[]): { id: string; name: string; locked: boolean }[] | null {
  if (list.length <= 1) return null;
  return sortOffices(list).map((o) => ({ id: o.id, name: o.name, locked: !o.can_enter }));
}
