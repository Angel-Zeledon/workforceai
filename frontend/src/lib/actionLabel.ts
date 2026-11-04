/**
 * Nothing technical on screen (proposal 5.4 #7): an action code ("email.send", "draft_reply", "gmail.send_reply")
 * is shown by its name in words. Pure: the caller passes the catalog lookup.
 * Order: action.<code> (dots as underscores), the project approval group pv.group.<code>, the verb alone
 * (action.<last part>), and last a neutral "Acción del agente" (the code itself only in "Ver detalle").
 */
export function actionLabel(code: string | null | undefined, lookup: (key: string) => string | undefined): string {
  const c = String(code ?? "").trim();
  const fallback = lookup("action.unknown") ?? "";
  if (!c) return fallback;
  const flat = c.replace(/[^a-zA-Z0-9]+/g, "_").toLowerCase();
  const verb = c.split(".").pop()!.replace(/[^a-zA-Z0-9]+/g, "_").toLowerCase();
  for (const k of [`action.${flat}`, `pv.group.${c}`, `action.${verb}`]) {
    const v = lookup(k);
    if (v) return v;
  }
  return fallback;
}

/** True when the code had no name in words (the screen then offers "Ver detalle" with the code). */
export const actionIsUnnamed = (code: string | null | undefined, lookup: (key: string) => string | undefined) =>
  actionLabel(code, lookup) === (lookup("action.unknown") ?? "");
