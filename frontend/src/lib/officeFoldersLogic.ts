/** Office folders in Files and in chat attachments (tests: officeFoldersLogic.test.mjs). Pure, no React. */

/** Roots of the drive that have a translated name (everything else keeps its own name). */
const ROOTS = new Set(["Agents", "Projects", "Clients", "Shared"]);

/** Label of a root folder: Offices has its own key, the other roots keep `drive.rootName.*`, any other name stays. */
export function rootLabel(name: string, t: (k: string) => string): string {
  if (name === "Offices") return t("drive.root.offices");
  return ROOTS.has(name) ? t(`drive.rootName.${name}`) : name;
}

/** Where the files of a chat go by default: the agent's office folder (`<folder>/<word>/<day>`) or Shared/Chat. */
export function attachDest(officeFolder: string | undefined, today: string, word: string): string {
  const base = (officeFolder ?? "").replace(/\/+$/, "");
  return base ? `${base}/${word}/${today}` : `/Shared/Chat/${today}`;
}

interface OfficeLike { visibility?: string; general?: boolean; folder_path?: string }

/** Folder paths of the private offices (not General) that have a folder: they show a lock in Files. */
export function privateOfficeFolders(offices: OfficeLike[]): Set<string> {
  return new Set(offices.filter((o) => o.visibility === "private" && !o.general && o.folder_path).map((o) => o.folder_path as string));
}

/** Folder of the office of the agent a conversation ("agent:<id>") is with; undefined if none. */
export function officeFolderOfAgent(conv: string, directory: { id: string; office_id?: string }[], offices: { id: string; folder_path?: string }[]): string | undefined {
  if (!conv.startsWith("agent:")) return undefined;
  const a = directory.find((d) => d.id === conv.slice(6));
  const f = a?.office_id ? offices.find((o) => o.id === a.office_id)?.folder_path : undefined;
  return f || undefined;
}
