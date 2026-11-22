/**
 * Pure logic of "Puestos" (positions inside a department). No imports, so node --test runs it.
 *
 * A position carries a package: skills + a library of folders (path, level read/edit/none). A position that reports to
 * another one never has a skill, a folder or a level that its superior does not have.
 *
 * Library semantics: an EMPTY library means "what the agent always saw" (the base: the department folder with Edit,
 * /Shared with Read and every folder shared into the department with Read). A NON-empty library is the whole list of
 * what is seen: the deepest entry that covers a path decides its level, and a path no entry covers is "none".
 */

export type Level = "read" | "edit" | "none";
export interface LibraryEntry { path: string; level: Level }
export type Template = "" | "manager" | "assistant" | "specialist";

export interface Position {
  id: string; office_id: string; name: string; reports_to: string; skills: string[]; library: LibraryEntry[];
  template: Template; occupants: string[]; created_by: string; created_at: string; updated_by: string; updated_at: string;
}
export interface Bounds { office_folder: string; shared: string; shared_in: string[] }

/** What a position has, as the rules compare it (a draft or a saved Position). */
export interface Package { skills: string[]; library: LibraryEntry[] }

export interface FolderLoss { path: string; from: Level | "default"; to: Level }
export interface Change { position_id: string; name: string; skills_removed: string[]; folders: FolderLoss[] }
export interface Impact { changed: Change[] }

const RANK: Record<Level, number> = { none: 0, read: 1, edit: 2 };
export const LEVELS: Level[] = ["read", "edit", "none"];

const norm = (p: string) => {
  const s = p.trim().replace(/\/+$/, "");
  return (s || "/").toLowerCase();
};

/** `path` is `root` or lives below it (case-insensitive, trailing slashes ignored). */
export function covers(root: string, path: string): boolean {
  const r = norm(root), p = norm(path);
  if (r === "/") return true;
  return p === r || p.startsWith(r + "/");
}

/** The base of a department: what an agent sees with an empty library. */
export function baseLibrary(b: Bounds): LibraryEntry[] {
  return [
    { path: b.office_folder, level: "edit" },
    { path: b.shared, level: "read" },
    ...b.shared_in.map((p) => ({ path: p, level: "read" as Level })),
  ];
}

/** Effective level of `path` in a library; the deepest entry that covers it wins. Empty library = the base. */
export function levelOf(library: LibraryEntry[], path: string, b: Bounds): Level {
  const entries = library.length ? library : baseLibrary(b);
  let best: LibraryEntry | null = null;
  for (const e of entries) {
    if (!covers(e.path, path)) continue;
    if (!best || norm(e.path).length > norm(best.path).length) best = e;
  }
  return best ? best.level : "none";
}

const min = (a: Level, b: Level): Level => (RANK[a] <= RANK[b] ? a : b);

/** Paths worth comparing: every entry of the given libraries. */
function probes(libs: LibraryEntry[][]): string[] {
  const out = new Map<string, string>();
  for (const lib of libs) for (const e of lib) out.set(norm(e.path), e.path);
  return [...out.values()];
}

/** What `sub` has above `sup`: skills the superior lacks and folders where sub's level is higher than sup's. Same
 * rule as the server (backend/internal/positions/rules.go): an empty library is the base on both sides, so a
 * subordinate without a library under a manager with a narrower one does exceed. */
export function exceeds(sup: Package, sub: Package, b: Bounds): { skills: string[]; folders: { path: string; level: Level }[] } {
  const skills = sub.skills.filter((s) => !sup.skills.includes(s));
  const folders: { path: string; level: Level }[] = [];
  const eff = (l: LibraryEntry[]) => (l.length ? l : baseLibrary(b));
  for (const p of probes([eff(sub.library), eff(sup.library)])) {
    const mine = levelOf(sub.library, p, b);
    if (RANK[mine] > RANK[levelOf(sup.library, p, b)]) folders.push({ path: p, level: mine });
  }
  folders.sort((x, y) => x.path.localeCompare(y.path));
  return { skills, folders };
}

export const exceedsAnything = (e: ReturnType<typeof exceeds>) => e.skills.length > 0 || e.folders.length > 0;

/** The most a position may give a path, given its superior (no superior = Edit anywhere inside the bounds). */
export function ceilingFor(sup: Package | null, path: string, b: Bounds): Level {
  return sup ? levelOf(sup.library, path, b) : "edit";
}

/** Where Edit makes no sense: folders shared into the department are read-only. */
export function readOnlyRoot(path: string, b: Bounds): boolean {
  return b.shared_in.some((r) => covers(r, path));
}

/** Highest level a draft may choose for a path: bounded by the superior and by read-only roots. */
export function maxLevelFor(sup: Package | null, path: string, b: Bounds): Level {
  const c = ceilingFor(sup, path, b);
  return readOnlyRoot(path, b) ? min(c, "read") : c;
}

/**
 * Default cut for a subordinate of `sup`: the same folders with Edit lowered to Read (exclusions kept) and NO skills
 * ticked (the person chooses which of the superior's skills to pass down). If the superior has no library (it sees
 * the base) the proposal is the base as explicit Read entries, so the subordinate starts read-only.
 */
export function proposeFromSuperior(sup: Package, b: Bounds): Package {
  const src = sup.library.length ? sup.library : baseLibrary(b);
  return { skills: [], library: src.map((e) => ({ path: e.path, level: e.level === "edit" ? "read" : e.level })) };
}

/** Drops from `draft` everything above the superior: skills it lacks, folders it can't see, levels lowered. */
export function clampToSuperior(sup: Package, draft: Package, b: Bounds): Package {
  const library: LibraryEntry[] = [];
  const src = draft.library.length ? draft.library : baseLibrary(b);
  for (const e of src) {
    if (e.level === "none") { library.push(e); continue; }
    const top = levelOf(sup.library, e.path, b);
    if (top === "none") continue;
    library.push({ path: e.path, level: min(e.level, top) });
  }
  // what the superior excludes or lowers below a kept folder stays excluded or lowered for the subordinate too
  const supLib = sup.library.length ? sup.library : baseLibrary(b);
  for (const e of supLib) {
    if (library.some((x) => norm(x.path) === norm(e.path))) continue;
    const mine = levelOf(library, e.path, b);
    if (RANK[mine] > RANK[e.level] && library.some((x) => x.level !== "none" && covers(x.path, e.path))) library.push({ path: e.path, level: e.level });
  }
  if (!library.length) library.push({ path: b.shared, level: "none" }); // nothing left stays explicit (empty = the base)
  return { skills: draft.skills.filter((s) => sup.skills.includes(s)), library };
}

/** Sets (or, with null, removes) the entry of `path`. Keeps the list sorted. */
export function setEntry(lib: LibraryEntry[], path: string, level: Level | null): LibraryEntry[] {
  const rest = lib.filter((e) => norm(e.path) !== norm(path));
  return sortLibrary(level ? [...rest, { path, level }] : rest);
}

export const sortLibrary = (lib: LibraryEntry[]): LibraryEntry[] => [...lib].sort((a, b) => a.path.localeCompare(b.path));

export function toggleSkill(skills: string[], id: string): string[] {
  return skills.includes(id) ? skills.filter((s) => s !== id) : [...skills, id];
}

export function sameLibrary(a: LibraryEntry[], b: LibraryEntry[]): boolean {
  const k = (l: LibraryEntry[]) => sortLibrary(l).map((e) => `${norm(e.path)}=${e.level}`).join("|");
  return k(a) === k(b);
}

/** Has the draft changed against what is saved? (name, superior, skills as a set, library as a set) */
export function isDirty(p: Position | null, d: { name: string; reports_to: string } & Package): boolean {
  if (!p) return true;
  return p.name.trim() !== d.name.trim() || p.reports_to !== d.reports_to
    || [...p.skills].sort().join(",") !== [...d.skills].sort().join(",") || !sameLibrary(p.library, d.library);
}

/** The path the level was named for the person: the part below the department folder ("" -> department itself). */
export function relativePath(path: string, b: Bounds): string {
  const root = [b.office_folder, b.shared, ...b.shared_in].find((r) => covers(r, path));
  if (!root) return path;
  const rel = path.slice(root.replace(/\/+$/, "").length).replace(/^\/+/, "");
  return rel;
}

// ---- hierarchy ---------------------------------------------------------------------------------------------------

export interface TreeRow { position: Position; depth: number }

/** Manager on top, subordinates indented below, siblings by name. A broken or cyclic link is shown at the top level. */
export function orderTree(positions: Position[]): TreeRow[] {
  const byId = new Map(positions.map((p) => [p.id, p]));
  const kids = new Map<string, Position[]>();
  const roots: Position[] = [];
  for (const p of positions) {
    if (p.reports_to && byId.has(p.reports_to) && p.reports_to !== p.id) {
      const l = kids.get(p.reports_to) ?? [];
      l.push(p); kids.set(p.reports_to, l);
    } else roots.push(p);
  }
  const by = (a: Position, b: Position) => a.name.localeCompare(b.name);
  const out: TreeRow[] = [];
  const seen = new Set<string>();
  const walk = (p: Position, depth: number) => {
    if (seen.has(p.id)) return;
    seen.add(p.id); out.push({ position: p, depth });
    for (const c of (kids.get(p.id) ?? []).sort(by)) walk(c, depth + 1);
  };
  for (const r of roots.sort(by)) walk(r, 0);
  for (const p of [...positions].sort(by)) walk(p, 0); // cycles: whatever is left
  return out;
}

/** Ids below `id` (its subordinates, their subordinates...). */
export function descendantsOf(positions: Position[], id: string): string[] {
  const out: string[] = [];
  const queue = [id];
  while (queue.length) {
    const cur = queue.shift()!;
    for (const p of positions) if (p.reports_to === cur && !out.includes(p.id) && p.id !== id) { out.push(p.id); queue.push(p.id); }
  }
  return out;
}

/** Positions that can be chosen as superior of `id` (not itself, not one of its subordinates). */
export function superiorChoices(positions: Position[], id: string | null): Position[] {
  const blocked = new Set(id ? [id, ...descendantsOf(positions, id)] : []);
  return orderTree(positions).map((r) => r.position).filter((p) => !blocked.has(p.id));
}

// ---- errors, labels, impact ---------------------------------------------------------------------------------------

const KNOWN_ERRORS = new Set([
  "name_required", "name_too_long", "name_taken", "too_many", "reports_to_invalid", "cycle", "skill_invalid",
  "skill_other_department", "folder_invalid", "folder_outside", "folder_level_invalid", "too_many_folders",
  "exceeds_superior", "position_other_office", "not_found", "forbidden", "office_unavailable",
]);

/** i18n key of what to tell the person for a refused call. Never the raw code or message. */
export function errorKey(code?: string | null, status?: number): string {
  if (status === 404 && (!code || code === "not_found")) return "pos.err.notFoundOffice";
  if (code === "humans_only") return "pos.err.forbidden";
  if (code === "too_many_skills") return "pos.err.too_many_skills";
  if (code && KNOWN_ERRORS.has(code)) return `pos.err.${code}`;
  return "pos.err.generic";
}

/** i18n key of where an agent's skill comes from. */
export function viaKey(via: string | undefined): string {
  return via === "position" ? "pos.via.position" : via === "role" ? "pos.via.role" : "pos.via.agent";
}

/** i18n key of where a folder rule comes from (the access list's `source`, or "fixed"). */
export function accessSourceKey(source: string | undefined, fixed?: boolean): string {
  if (fixed) return "pos.src.fixed";
  return source === "position" ? "pos.src.position" : source === "manual" ? "pos.src.manual" : "pos.src.default";
}

/** i18n key of a level. */
export const levelLabelKey = (l: Level | "default") => `pos.level.${l}`;

/** i18n key of a template's visible name (the frontend names it; {dept} fills the manager's). */
export const templateNameKey = (k: string) => `pos.template.${k}`;

/** Flattens an impact into one line per subordinate (who, what skills, what folders), for the confirmation. */
export function impactLines(impact: Impact | null | undefined): Change[] {
  return (impact?.changed ?? []).filter((c) => c.skills_removed.length || c.folders.length);
}

/** Reads the `details` of an exceeds_superior error without trusting its shape. */
export function exceedsDetails(d: unknown): { skills: string[]; folders: { path: string; level: Level }[] } {
  const o = (d ?? {}) as { skills?: unknown; folders?: unknown };
  const skills = Array.isArray(o.skills) ? o.skills.filter((s): s is string => typeof s === "string") : [];
  const folders = Array.isArray(o.folders)
    ? o.folders.filter((f): f is { path: string; level: Level } => !!f && typeof (f as { path?: unknown }).path === "string").map((f) => ({ path: f.path, level: f.level }))
    : [];
  return { skills, folders };
}

/** The position an agent holds in the list, if any. */
export const positionOfAgent = (positions: Position[], agentId: string): Position | null =>
  positions.find((p) => p.occupants.includes(agentId)) ?? null;
