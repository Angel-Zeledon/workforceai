/**
 * The Files screen in plain words (tests: filesLogic.test.mjs). Pure, no React. The drive keeps its technical roots
 * (/Offices, /Shared, /Projects, /Clients, /Agents); here they become sections a person understands ("Mis
 * departamentos", "Compartido con todos"...), breadcrumbs by name, the system's own folders hidden, and the rules of
 * what can be created, renamed or moved where. Paths compare case-insensitively, as the drive resolves them.
 */

const norm = (p: string) => (p.replace(/\/+$/, "") || "/").toLowerCase();
const segs = (p: string) => p.split("/").filter(Boolean);
export const parentPath = (p: string) => { const s = segs(p); return s.length <= 1 ? "/" : "/" + s.slice(0, -1).join("/"); };
const baseName = (p: string) => segs(p).pop() ?? "";

type T = (k: string, v?: Record<string, string | number>) => string;

/** What the screen needs of an office (OfficeView has it all). */
export interface OfficeRef {
  id: string; name: string; folder_path?: string; is_member?: boolean; general?: boolean; visibility?: string;
  leads?: { name?: string | null }[];
}

export interface NameCtx {
  offices: OfficeRef[];
  /** The agent's name, or undefined when the agent is not known (a desk is never shown by its id). */
  agentName: (id: string) => string | undefined;
  t: T;
  locale?: string;
}

export type Section = "home" | "offices" | "dept" | "shared" | "projects" | "clients" | "agents";

const ROOT_SECTION: Record<string, Section> = { offices: "dept", shared: "shared", projects: "projects", clients: "clients", agents: "agents" };
export const SECTION_PATH: Record<Exclude<Section, "home" | "dept">, string> = {
  offices: "/Offices", shared: "/Shared", projects: "/Projects", clients: "/Clients", agents: "/Agents",
};
/** The label key of each section (the order of the side bar). */
export const SECTION_LABEL: Record<Section, string> = {
  home: "files.title", offices: "files.section.depts", dept: "files.section.depts", shared: "files.section.shared",
  projects: "files.section.projects", clients: "files.section.clients", agents: "files.section.agents",
};

/** The section a path belongs to ("/Offices" itself is the list of departments). */
export function sectionOf(path: string): Section {
  const s = segs(path);
  if (!s.length) return "home";
  const sec = ROOT_SECTION[s[0].toLowerCase()];
  if (!sec) return "home";
  return sec === "dept" && s.length === 1 ? "offices" : sec;
}

/** The office whose folder holds `path` (or is it). */
export function officeOfPath<O extends OfficeRef>(path: string, offices: O[]): O | undefined {
  const p = norm(path);
  return offices.find((o) => {
    if (!o.folder_path) return false;
    const r = norm(o.folder_path);
    return p === r || p.startsWith(r + "/");
  });
}

/** The root of the place a path lives in: the department's folder, or the section's root. */
export function placeRoot(path: string, offices: OfficeRef[]): string {
  const sec = sectionOf(path);
  if (sec === "home") return "/";
  if (sec === "dept") {
    const o = officeOfPath(path, offices);
    return o?.folder_path?.replace(/\/+$/, "") ?? "/" + segs(path).slice(0, 2).join("/");
  }
  return SECTION_PATH[sec];
}

// ------------------------------------------------------------------ the system's folders

/** Folders the platform keeps for itself: skills and their versions live in /Shared/Skills (seen from Habilidades). */
const HIDDEN = ["/shared/skills"];

/** The path is (or lies under) a folder the normal navigation does not show. */
export function isHiddenPath(path: string): boolean {
  const p = norm(path);
  return HIDDEN.some((h) => p === h || p.startsWith(h + "/"));
}

/** The folders a listing shows: without the system's own. */
export function visibleFolders<F extends { path: string }>(folders: F[]): F[] {
  return folders.filter((f) => !isHiddenPath(f.path));
}

/** The word the chat uses for its attachment folder in each language (officeFoldersLogic.attachDest). */
export const ATTACH_WORDS = ["adjuntos", "attachments", "anexos"];

/** The folder where the chat leaves attachments: /Shared/Chat, or <department>/Adjuntos (any of the three words). */
export function isAttachmentsFolder(path: string): boolean {
  const s = segs(path).map((x) => x.toLowerCase());
  if (s.length === 2 && s[0] === "shared" && s[1] === "chat") return true;
  return s.length === 3 && s[0] === "offices" && ATTACH_WORDS.includes(s[2]);
}

/** A day folder of the attachments ("2026-11-21"). */
export const isDayName = (name: string) => /^\d{4}-\d{2}-\d{2}$/.test(name);

/** The local date a day folder names (null when it is not one). */
export function dayOf(name: string): Date | null {
  if (!isDayName(name)) return null;
  const [y, m, d] = name.split("-").map(Number);
  const dt = new Date(y, m - 1, d);
  return dt.getMonth() === m - 1 ? dt : null;
}

// ------------------------------------------------------------------ names and breadcrumbs

function dayLabel(name: string, locale?: string): string {
  const d = dayOf(name);
  if (!d) return name;
  try { return new Intl.DateTimeFormat(locale, { day: "numeric", month: "long", year: "numeric" }).format(d); } catch { return name; }
}

/** The name a person reads for a folder. */
export function folderLabel(path: string, ctx: NameCtx): string {
  const s = segs(path);
  if (!s.length) return ctx.t("files.title");
  const top = s[0].toLowerCase();
  if (s.length === 1) {
    const sec = sectionOf(path);
    return sec === "home" ? s[0] : ctx.t(SECTION_LABEL[sec]);
  }
  if (top === "offices" && s.length === 2) return officeOfPath(path, ctx.offices)?.name ?? s[1];
  if (top === "agents" && s.length === 2) return ctx.agentName(s[1]) ?? ctx.t("files.agent.unknown");
  if (isAttachmentsFolder(path)) return ctx.t("files.attachments");
  if (isAttachmentsFolder(parentPath(path))) return dayLabel(s[s.length - 1], ctx.locale);
  return s[s.length - 1];
}

export interface Crumb { label: string; path: string }

/**
 * Breadcrumbs by name: a department starts at its own name ("Contabilidad › Bancos y conciliaciones"), the rest at
 * their section ("Escritorios de los agentes › Tomás Vidal"). The home has none.
 */
export function humanCrumbs(path: string, ctx: NameCtx): Crumb[] {
  const s = segs(path);
  if (!s.length) return [];
  const sec = sectionOf(path);
  const start = sec === "dept" ? 2 : 1;
  const out: Crumb[] = [];
  for (let i = start; i <= s.length; i++) {
    const p = "/" + s.slice(0, i).join("/");
    out.push({ label: folderLabel(p, ctx), path: p });
  }
  return out;
}

export interface Desk { id: string; name: string; path: string; known: boolean }

/** The agents' desks (/Agents/<id>) by name, sorted; an agent nobody knows anymore keeps a plain label, never its id. */
export function agentDesks(folders: { name: string; path: string }[], ctx: Pick<NameCtx, "agentName" | "t">): Desk[] {
  return folders.map((f) => {
    const n = ctx.agentName(f.name);
    return { id: f.name, name: n ?? ctx.t("files.agent.unknown"), path: f.path, known: n !== undefined };
  }).sort((a, b) => Number(b.known) - Number(a.known) || a.name.localeCompare(b.name));
}

/** The departments of the side bar: mine (member) first, then the open ones the person may read. Never General. */
export function navDepartments<O extends OfficeRef>(offices: O[]): { mine: O[]; others: O[] } {
  const withFolder = offices.filter((o) => !o.general && !!o.folder_path);
  return { mine: withFolder.filter((o) => o.is_member), others: withFolder.filter((o) => !o.is_member && o.visibility !== "private") };
}

// ------------------------------------------------------------------ help lines

export interface Msg { key: string; vars?: Record<string, string> }

/** One line under the title that says what goes in this place. */
export function sectionHelp(path: string, ctx: Pick<NameCtx, "offices">): Msg {
  if (isAttachmentsFolder(path)) return { key: "files.help.attachments" };
  const sec = sectionOf(path);
  if (sec === "dept") {
    const o = officeOfPath(path, ctx.offices);
    const name = o?.name ?? segs(path)[1] ?? "";
    if (o && !o.is_member) return { key: "files.help.deptReader", vars: { name } };
    return { key: o?.visibility === "private" ? "files.help.deptPrivate" : "files.help.dept", vars: { name } };
  }
  return { key: `files.help.${sec}` };
}

// ------------------------------------------------------------------ what can be changed where

/** Places only the platform creates folders in: the home, the list of departments, of agents and of projects. */
const SYSTEM_TOPS = new Set(["/", "/offices", "/agents", "/projects"]);

/** "Nueva carpeta" / "Subir" are possible here: the person edits and the place is not one the platform fills. */
export function canCreateHere(path: string, access: string | undefined): boolean {
  return access === "edit" && !SYSTEM_TOPS.has(norm(path));
}

/** Why "Nueva carpeta" / "Subir" are off here, in words (null when they are on). */
export function readOnlyReason(path: string, access: string | undefined, ctx: Pick<NameCtx, "offices">, sharedBy?: string): Msg | null {
  const p = norm(path);
  if (p === "/" || p === "/offices") return { key: "files.ro.pick" };
  if (p === "/agents") return { key: "files.ro.agents" };
  if (p === "/projects") return { key: "files.ro.projects" };
  if (access === "edit") return null;
  if (sharedBy) return { key: "files.ro.shared", vars: { office: sharedBy } };
  if (sectionOf(path) === "dept") {
    const o = officeOfPath(path, ctx.offices);
    const name = o?.name ?? segs(path)[1] ?? "";
    const lead = o?.leads?.map((l) => l.name?.trim()).find(Boolean);
    return lead ? { key: "files.ro.deptLead", vars: { name, lead } } : { key: "files.ro.dept", vars: { name } };
  }
  return { key: "files.ro.role" };
}

/** A folder the platform owns (a section, a department's, an agent's, a project's or a client's own folder, the chat's
 * attachments): it can be opened but not renamed nor moved. The backend refuses it too (protected_folder). */
export function isProtectedFolder(f: { path: string; ref_id?: string }): boolean {
  const s = segs(f.path);
  if (s.length <= 1 || f.ref_id) return true;
  const top = s[0].toLowerCase();
  if (s.length === 2 && (top === "offices" || top === "agents" || top === "projects")) return true;
  return isAttachmentsFolder(f.path) || isHiddenPath(f.path);
}

// forbidden characters, control and invisible ones (zero-width, bidi overrides, line separators), as drive.CleanName
const FORBIDDEN = new RegExp("[/\\\\:*?\"<>|\u0000-\u001f\u007f-\u009f\u200b-\u200f\u202a-\u202e\u2028\u2029\u2060-\u2064\ufeff]");
const ext = (n: string) => { const i = n.lastIndexOf("."); return i > 0 ? n.slice(i + 1).toLowerCase() : ""; };

/**
 * The name for a new folder or a rename, checked as the drive checks it (drive.CleanName) plus a duplicate among the
 * siblings (the drive would silently reuse an existing folder). Returns the error key, or null when the name is good.
 * `original`: the current name (a rename); a file keeps its extension.
 */
export function nameError(raw: string, siblings: string[], opts: { original?: string; file?: boolean } = {}): string | null {
  const name = raw.trim();
  if (!name) return "files.name.empty";
  if ([...name].length > 120) return "files.name.long";
  if (FORBIDDEN.test(name)) return "files.name.chars";
  if (/^\.+$/.test(name) || name.endsWith(".")) return "files.name.dots";
  if (opts.file && opts.original && ext(opts.original) !== ext(name)) return "files.name.ext";
  const k = name.normalize("NFC").toLowerCase();
  const orig = opts.original?.normalize("NFC").toLowerCase();
  if (k !== orig && siblings.some((s) => s.normalize("NFC").toLowerCase() === k)) return "files.name.taken";
  return null;
}

/** Whether `dest` can receive the item at `src` (its new parent). Returns the error key or null. */
export function moveError(src: { kind: "file" | "folder"; path: string }, dest: string): string | null {
  const d = norm(dest), s = norm(src.path);
  if (SYSTEM_TOPS.has(d)) return "files.move.system";
  if (src.kind === "folder" && (d === s || d.startsWith(s + "/"))) return "files.move.inside";
  if (d === norm(parentPath(src.path))) return "files.move.same";
  return null;
}

/** A warning when a move changes who sees the item (it leaves a department, enters another one or Shared). */
export function moveWarning(srcPath: string, dest: string, ctx: Pick<NameCtx, "offices" | "t">): Msg | null {
  const from = officeOfPath(srcPath, ctx.offices), to = officeOfPath(dest, ctx.offices);
  if (from?.id === to?.id && sectionOf(srcPath) === sectionOf(dest)) return null;
  if (to) return { key: "files.move.warn.dept", vars: { name: to.name } };
  if (sectionOf(dest) === "shared") return { key: "files.move.warn.shared" };
  if (from) return { key: "files.move.warn.out", vars: { name: from.name } };
  return null;
}

/** The new path of an item moved into `dest` (and/or renamed to `name`). */
export const joinPath = (dest: string, name: string) => `${norm(dest) === "/" ? "" : dest.replace(/\/+$/, "")}/${name}`;

// ------------------------------------------------------------------ the chat's attachments by date

export type DateGroup = "today" | "week" | "month" | "older";
export const DATE_GROUPS: DateGroup[] = ["today", "week", "month", "older"];

/** Today, the last 7 days, the last 30 days, or before. */
export function dateGroup(d: Date, now: Date): DateGroup {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const t = d.getTime();
  if (t >= start) return "today";
  if (t >= start - 6 * 86_400_000) return "week";
  if (t >= start - 29 * 86_400_000) return "month";
  return "older";
}

/** When an attachment arrived: the day of its folder (<attachments>/<day>/name), else its last change. */
export function attachedAt(f: { path: string; updated_at: string }): Date {
  return dayOf(baseName(parentPath(f.path))) ?? new Date(f.updated_at);
}

/** The attachments grouped under "Hoy", "Esta semana"... newest first; empty groups are left out. */
export function groupAttachments<F extends { path: string; updated_at: string }>(files: F[], now: Date): { group: DateGroup; files: F[] }[] {
  const by = new Map<DateGroup, F[]>();
  const sorted = [...files].sort((a, b) => attachedAt(b).getTime() - attachedAt(a).getTime() || Date.parse(b.updated_at) - Date.parse(a.updated_at));
  for (const f of sorted) {
    const g = dateGroup(attachedAt(f), now);
    by.set(g, [...(by.get(g) ?? []), f]);
  }
  return DATE_GROUPS.filter((g) => by.has(g)).map((g) => ({ group: g, files: by.get(g)! }));
}

/** The day folders of an attachments folder, newest first, at most `max` (the view lists their files). */
export function dayFolders<F extends { name: string }>(folders: F[], max = 60): { days: F[]; others: F[] } {
  const days = folders.filter((f) => dayOf(f.name)).sort((a, b) => b.name.localeCompare(a.name));
  return { days: days.slice(0, max), others: folders.filter((f) => !dayOf(f.name)) };
}

// ------------------------------------------------------------------ URLs

/** The Files URL of a folder: a department opens at /d/<office>?carpeta=, everything else at /files?path=. */
export function folderHref(path: string, offices: OfficeRef[]): string {
  const p = path.replace(/\/+$/, "") || "/";
  if (p === "/") return "/files";
  const o = sectionOf(p) === "dept" ? officeOfPath(p, offices) : undefined;
  if (o?.folder_path) {
    const root = o.folder_path.replace(/\/+$/, "");
    const rel = norm(p) === norm(root) ? "" : p.slice(root.length + 1);
    return `/d/${encodeURIComponent(o.id)}${rel ? `?carpeta=${encodeURIComponent(rel)}` : ""}`;
  }
  return `/files?path=${encodeURIComponent(p)}`;
}

// ------------------------------------------------------------------ errors in words

/** The text key for a refused drive action (a DriveError's status and stable code): never the raw code. */
export function filesErrorKey(e: unknown): string {
  const o = (e && typeof e === "object" ? e : {}) as { code?: unknown; status?: unknown };
  const code = typeof o.code === "string" ? o.code : "";
  const status = typeof o.status === "number" ? o.status : 0;
  switch (code) {
    case "read_only_folder": case "read_only_mode": return "files.err.readOnly";
    case "protected_folder": return "files.err.protected";
    case "name_taken": case "exists": case "already_exists": return "files.err.conflict";
    case "extension_change": return "files.name.ext";
    case "name_invalid": case "path_invalid": return "files.err.invalid";
    case "plan_limit": return "drive.planLimit";
    case "quota_exceeded": case "quota": return "files.err.quota";
    case "too_large": return "files.err.tooLarge";
    case "unsupported_type": return "files.err.type";
  }
  if (status === 403 || status === 423) return "files.err.readOnly";
  if (status === 404) return "files.err.notFound";
  if (status === 409) return "files.err.conflict";
  if (status === 413) return "files.err.tooLarge";
  if (status === 415) return "files.err.type";
  if (status === 400) return "files.err.invalid";
  return "files.err.generic";
}
