/** Pure helpers of the folder access of agents ("Carpetas que puede ver", "Qué agentes la ven"): no imports, so the
 * unit tests run them under node --test. Levels: none (Ninguno), read (Leer), edit (Editar); inherited downwards, the
 * most specific rule wins (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 3). */
export type AccessLevel = "none" | "read" | "edit";
export type AccessSource = "default" | "manual";
export interface AccessEntry { path: string; level: AccessLevel; source: AccessSource; grant_id?: string; fixed?: boolean }
export interface FolderAgent { agent_id: string; name: string; level: AccessLevel; source: AccessSource; can_manage: boolean }

export const LEVELS: AccessLevel[] = ["none", "read", "edit"];

/** i18n key of a level ("access.level.read"). */
export const levelKey = (l: AccessLevel) => `access.level.${l}`;

/** What choosing a level sends: picking the level the folder already has by default removes the rule ("default"). */
export function nextLevel(entry: Pick<AccessEntry, "level" | "source">, chosen: AccessLevel, defaultLevel?: AccessLevel): AccessLevel | "default" {
  if (entry.source === "manual" && defaultLevel && chosen === defaultLevel) return "default";
  return chosen;
}

/** Default level of a folder for an agent (desk and its projects Edit, /Shared Read, the rest Ninguno). */
export function defaultLevelOf(path: string, agentId: string, projectRoots: string[] = []): AccessLevel {
  const under = (p: string, root: string) => p.toLowerCase() === root.toLowerCase() || p.toLowerCase().startsWith(root.toLowerCase() + "/");
  if (under(path, `/Agents/${agentId}`)) return "edit";
  if (projectRoots.some((r) => under(path, r))) return "edit";
  if (under(path, "/Shared")) return "read";
  return "none";
}

/** A folder an agent can be given access to: a clean absolute path below "/" that is not the agent's own desk. */
export function validFolder(path: string, agentId: string): boolean {
  const p = path.trim();
  if (!p.startsWith("/") || p === "/" || p.includes("..") || p.includes("//")) return false;
  return !(p === `/Agents/${agentId}` || p.startsWith(`/Agents/${agentId}/`));
}

/** Entries sorted for the screen: the fixed desk first, then by path. */
export function sortEntries(es: AccessEntry[]): AccessEntry[] {
  return [...es].sort((a, b) => (a.fixed === b.fixed ? a.path.localeCompare(b.path) : a.fixed ? -1 : 1));
}

/** Counts per level for the "Qué agentes la ven" header (e.g. 2 edit, 3 read, 1 none). */
export function countByLevel(as: FolderAgent[]): Record<AccessLevel, number> {
  const out: Record<AccessLevel, number> = { none: 0, read: 0, edit: 0 };
  for (const a of as) out[a.level] = (out[a.level] ?? 0) + 1;
  return out;
}

/** Shows the Drive's English roots with the names people see (/Shared -> /Compartido when the label exists). */
export function displayPath(path: string, roots: Record<string, string>): string {
  const segs = path.split("/").filter(Boolean);
  if (!segs.length) return "/";
  const head = roots[segs[0]] ?? segs[0];
  return "/" + [head, ...segs.slice(1)].join("/");
}
