"use client";
import { useCallback, useSyncExternalStore } from "react";
import { usePreferences } from "./preferences";
import { OFFICE_3D_NOTICE_KEY, OFFICE_VIEW_KEY, resolveView, type OfficeView } from "./officeViewLogic";

const EVT = "wf:office-view";
const read = (k: string): string | null => { try { return window.localStorage.getItem(k); } catch { return null; } };
const write = (k: string, v: string) => { try { window.localStorage.setItem(k, v); } catch { /* private mode */ } window.dispatchEvent(new Event(EVT)); };
const subscribe = (cb: () => void) => {
  window.addEventListener(EVT, cb);
  window.addEventListener("storage", cb);
  return () => { window.removeEventListener(EVT, cb); window.removeEventListener("storage", cb); };
};

/** The view Oficina is in and the way to change it. Choosing the 3D turns it on (no trip through Ajustes). */
export function useOfficeView(): { view: OfficeView; ready: boolean; setView: (v: OfficeView) => void } {
  const pref = usePreferences((s) => s.prefs.office3d);
  const ready = usePreferences((s) => s.hydrated);
  const stored = useSyncExternalStore(subscribe, () => read(OFFICE_VIEW_KEY), () => null);
  const setView = useCallback((v: OfficeView) => {
    if (v === "3d" && !usePreferences.getState().prefs.office3d) usePreferences.getState().set({ office3d: true });
    write(OFFICE_VIEW_KEY, v);
  }, []);
  return { view: resolveView(pref, stored), ready, setView };
}

/** The first time the 3D opens, a short dismissible notice says it uses more resources. */
export function useOffice3dNotice(): { show: boolean; dismiss: () => void } {
  const seen = useSyncExternalStore(subscribe, () => read(OFFICE_3D_NOTICE_KEY), () => "1");
  return { show: seen !== "1", dismiss: () => write(OFFICE_3D_NOTICE_KEY, "1") };
}
