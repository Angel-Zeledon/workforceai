/**
 * E1 "Alta y agentes por rol" (docs/architecture/equipos-oficinas.md §6): pure logic of the team sign-up, the welcome per
 * case and the team set-up. Contract: backend/internal/auth/teams.go and backend/internal/api/teams.go.
 */

export const AUDIENCES = ["solo", "team", "company"] as const;
export type Audience = (typeof AUDIENCES)[number];

/** Same keys and order as backend/internal/roles/professions.go. */
export const PROFESSIONS = ["legal", "sales", "accounting", "marketing", "operations", "hr", "direction", "education", "other"] as const;
export type Profession = (typeof PROFESSIONS)[number];

export const isProfession = (v: string): v is Profession => (PROFESSIONS as readonly string[]).includes(v);

export interface SignupForm {
  email: string; password: string; name: string; org: string; team: string;
  audience: Audience | ""; profession: Profession | "";
  /** The address can enter its company (verified domain) and the person chose to. */
  join: boolean;
  locale?: string;
}

export interface RegisterBody {
  email: string; password: string; name: string; org_name: string; locale?: string;
  audience: Audience; profession: Profession; team_name?: string; join?: boolean;
}

/** Fields still missing before the form can be sent (keys of the inputs). */
export function signupMissing(f: SignupForm): string[] {
  const out: string[] = [];
  if (!f.email.includes("@")) out.push("email");
  if (!f.name.trim()) out.push("name");
  if (!f.password) out.push("password");
  if (!f.profession) out.push("profession");
  if (!f.join) {
    if (!f.audience) out.push("audience");
    if ((f.audience === "team" || f.audience === "company") && f.org.trim().length < 2) out.push("org");
  }
  return out;
}

/** The body of POST /auth/register. A personal office (and a join) needs no organization name: it takes the person's. */
export function registerBody(f: SignupForm): RegisterBody {
  const audience: Audience = f.join ? "solo" : (f.audience || "solo");
  const body: RegisterBody = {
    email: f.email.trim(), password: f.password, name: f.name.trim(),
    org_name: f.join || audience === "solo" ? "" : f.org.trim(),
    audience, profession: f.profession || "other",
  };
  if (f.locale) body.locale = f.locale;
  if (!f.join && audience === "team" && f.team.trim()) body.team_name = f.team.trim();
  if (f.join) body.join = true;
  return body;
}

// ---- welcome ----

export type WelcomeKind = "solo" | "owner" | "lead" | "member" | "classic";

export interface WelcomeAgent { id: string; name: string; title: string; role: string }
export interface WelcomeTeam { id: string; name: string; head_name: string; head_is_me: boolean }
export interface Welcome {
  kind: WelcomeKind; audience: Audience | ""; org_name: string; setup_pending: boolean; profession: string;
  team: WelcomeTeam | null; invited_by: string; agents: WelcomeAgent[]; teammates: { user_id: string; name: string }[];
  can_pick_team: boolean; teams: { id: string; name: string }[];
}

export type WelcomeAction = "start" | "setup" | "pickTeam";

export interface WelcomePlan {
  /** i18n key prefix of the variant: e1.welcome.<variant>.title / .sub / .stepN.t / .stepN.d */
  variant: "solo" | "owner" | "ownerTeam" | "lead" | "member" | "memberNoTeam";
  primary: WelcomeAction;
  /** The tour offered as the secondary button. */
  tour: boolean;
}

/** Which welcome a person sees, and what its main button does (null = the classic welcome). */
export function welcomePlan(w: Welcome | null): WelcomePlan | null {
  if (!w || w.kind === "classic") return null;
  switch (w.kind) {
    case "solo":
      return { variant: "solo", primary: "start", tour: true };
    case "owner":
      return { variant: w.audience === "team" ? "ownerTeam" : "owner", primary: w.setup_pending ? "setup" : "start", tour: !w.setup_pending };
    case "lead":
      return { variant: "lead", primary: w.can_pick_team ? "pickTeam" : "start", tour: true };
    default:
      return { variant: w.can_pick_team ? "memberNoTeam" : "member", primary: w.can_pick_team ? "pickTeam" : "start", tour: !w.can_pick_team };
  }
}

/** Agents of the welcome as a short list ("Elena Castro (Abogada), Sofía Lara (Asistente)"). */
export const agentList = (agents: WelcomeAgent[]): string => agents.map((a) => (a.title ? `${a.name} (${a.title})` : a.name)).join(", ");

// ---- team set-up (responsible) ----

export type HeadChoice = "me" | "invite" | "later";

export interface TeamRow { key: string; name: string; profession: Profession; head: HeadChoice; email: string; on: boolean }

export interface InviteRow { email: string; profession: Profession }

const emailOk = (v: string) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v.trim());

/** Problems of the company's team list before creating it (row key -> i18n error key). */
export function teamRowsIssues(rows: TeamRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  const seen = new Set<string>();
  for (const r of rows) {
    if (!r.on) continue;
    const n = r.name.trim().toLowerCase();
    if (!n) out[r.key] = "e1.setup.err.name";
    else if (n.length > 80) out[r.key] = "e1.setup.err.long";
    else if (seen.has(n)) out[r.key] = "e1.setup.err.dup";
    else if (r.head === "invite" && !emailOk(r.email)) out[r.key] = "e1.setup.err.email";
    seen.add(n);
  }
  return out;
}

/** The suggested team list of a company: one row per profession (except "other"), all off. */
export function suggestedTeams(teamName: (p: Profession) => string): TeamRow[] {
  return PROFESSIONS.filter((p) => p !== "other").map((p) => ({ key: p, name: teamName(p), profession: p, head: "later" as HeadChoice, email: "", on: false }));
}

/** Invitations of a team set-up: valid, distinct addresses only (lower-cased), in order. */
export function cleanInvites(rows: InviteRow[]): InviteRow[] {
  const seen = new Set<string>();
  const out: InviteRow[] = [];
  for (const r of rows) {
    const e = r.email.trim().toLowerCase();
    if (!emailOk(e) || seen.has(e)) continue;
    seen.add(e);
    out.push({ email: e, profession: r.profession });
  }
  return out;
}

/** Splits pasted addresses ("a@x.com, b@x.com; c@x.com") into invite rows with one profession. */
export function parseEmails(text: string, profession: Profession): InviteRow[] {
  return text.split(/[\s,;]+/).filter(Boolean).map((email) => ({ email, profession }));
}
