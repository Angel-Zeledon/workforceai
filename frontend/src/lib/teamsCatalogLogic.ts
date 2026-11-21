/** F2 "Equipos listos para contratar": pure logic of the gallery and the preview (tests: teamsCatalogLogic.test.mjs).
 *  Contract: backend/internal/api/teams_catalog.go (GET /teams, GET /teams/{key}, POST /teams/{key}/install). */

export interface TeamCard {
  key: string; version: number; name: string; for: string; solves: string; department: string; packs: string[];
  agents: { role: string; title: string }[]; templates: number; new_count: number; existing_count: number;
}

export interface TeamPreviewAgent {
  role: string; title: string; does: string; status: "new" | "existing";
  /** suggested name of a new agent; "" when the role is already filled */
  name: string; autonomy: string; skills: string[];
  existing?: { id: string; name: string; title: string };
}
export interface TeamWeekItem { template: string; day: number; why: string; name: string; description: string }
export type BlockedReason = "nothing_new" | "office_full" | "plan_limit" | "";

export interface TeamPreview {
  team: { key: string; version: number; name: string; for: string; solves: string; department: string; packs: string[] };
  destination: { office_id: string; office_name: string; suggested_office_id: string };
  agents: TeamPreviewAgent[]; new_count: number; existing_count: number; skills: string[]; week: TeamWeekItem[];
  can_install: boolean; blocked_reason?: BlockedReason;
}

export interface TeamInstallResult { created: { id: string; name: string; role: string }[]; preview: TeamPreview }

/** The people who arrive and the ones the department already has ("Llegan estas personas" / "Ya tienes a…"). */
export function splitAgents(p: Pick<TeamPreview, "agents">) {
  return { arriving: p.agents.filter((a) => a.status === "new"), already: p.agents.filter((a) => a.status === "existing") };
}

/** The first week as days in order, each with the flows that start that day. */
export function weekByDay(week: TeamWeekItem[]): { day: number; items: TeamWeekItem[] }[] {
  const days = new Map<number, TeamWeekItem[]>();
  for (const w of week) days.set(w.day, [...(days.get(w.day) ?? []), w]);
  return [...days.entries()].sort((a, b) => a[0] - b[0]).map(([day, items]) => ({ day, items }));
}

/** What the hire button and its note say: null = hire it; otherwise the i18n key of why not. */
export function blockedKey(p: Pick<TeamPreview, "can_install" | "blocked_reason">): string | null {
  if (p.can_install) return null;
  switch (p.blocked_reason) {
    case "office_full": return "teams.blocked.officeFull";
    case "plan_limit": return "teams.blocked.planLimit";
    default: return "teams.blocked.nothingNew";
  }
}

/** The state line of a gallery card. */
export function cardState(c: Pick<TeamCard, "new_count" | "existing_count">): "all" | "some" | "none" {
  if (c.new_count === 0) return "all";
  return c.existing_count > 0 ? "some" : "none";
}

/** Cards that suit an onboarding pack first (the rest follow); no pack = the order the server sent. */
export function orderForPack(cards: TeamCard[], pack: string): TeamCard[] {
  if (!pack) return cards;
  const fits = (c: TeamCard) => c.packs.includes(pack);
  return [...cards.filter(fits), ...cards.filter((c) => !fits(c))];
}

interface OfficeChoice { id: string; name: string; general: boolean; can_enter: boolean; can_manage: boolean }

/** Departments a team can be hired into: General and the ones the person enters and manages (a private department the
 * person is not in never appears). General always comes first. */
export function destinationChoices(offices: OfficeChoice[] | undefined): { id: string; name: string }[] {
  const list = (offices ?? []).filter((o) => o.general || (o.can_enter && o.can_manage));
  const general = list.find((o) => o.general) ?? { id: "general", name: "General", general: true, can_enter: true, can_manage: true };
  return [general, ...list.filter((o) => !o.general)].map((o) => ({ id: o.id, name: o.name }));
}

/** The result line after hiring: who came, or that everybody was already there. */
export function installOutcome(r: Pick<TeamInstallResult, "created">): { key: "teams.installed" | "teams.alreadyThere"; count: number } {
  return r.created.length > 0 ? { key: "teams.installed", count: r.created.length } : { key: "teams.alreadyThere", count: 0 };
}
