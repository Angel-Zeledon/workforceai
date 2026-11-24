/** The four tabs of the agent's sheet (`/agents/<id>?tab=`). Each question has one tab; old section names land in the right one.
 *  Pure: tests in agentSheetLogic.test.mjs. */
export const SHEET_TABS = ["who", "where", "skills", "perms"] as const;
export type SheetTab = (typeof SHEET_TABS)[number];

const ALIAS: Record<string, SheetTab> = {
  profile: "who", quien: "who", perfil: "who",
  office: "where", offices: "where", oficina: "where", position: "where", puesto: "where", access: "where",
  skill: "skills", habilidades: "skills", sabe: "skills",
  permissions: "perms", permisos: "perms", permission: "perms", can: "perms", folders: "perms",
};

export function resolveSheetTab(raw: string | null | undefined): SheetTab {
  const k = String(raw ?? "").trim().toLowerCase();
  if ((SHEET_TABS as readonly string[]).includes(k)) return k as SheetTab;
  return ALIAS[k] ?? "who";
}
