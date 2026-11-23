/**
 * Which view Oficina opens in: "Lista" (the multi-agent workspace) or "Oficina 3D". Pure (no React): the 3D is opt-in
 * (it uses a lot of the device), so the list is the default; once the person turns the 3D on, the last view they used
 * is the one Oficina opens in. `prefs.office3d` is the switch of the 3D (shop, coins); the stored view is the choice.
 */
export type OfficeView = "list" | "3d";

export const OFFICE_VIEW_KEY = "wf.office.view";
export const OFFICE_3D_NOTICE_KEY = "wf.office.3dNotice";

/** The view to show: the list whenever the 3D is off; with it on, the stored one (the 3D when nothing is stored). */
export function resolveView(office3d: boolean, stored: string | null | undefined): OfficeView {
  if (!office3d) return "list";
  return stored === "list" ? "list" : "3d";
}

/** Whether the selector marks "Lista": the old `/dashboard` address is always the list. */
export function isListShown(section: string, view: OfficeView): boolean {
  return section === "dashboard" || (section === "office" && view === "list");
}
