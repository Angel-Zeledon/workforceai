"use client";
import { useEffect } from "react";
import { useT } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { ConnectionsView } from "../connections/ConnectionsView";
import { SecurityCenter } from "./SecurityCenter";

/** Loads connections/controls state once (also refreshed by WS frames routed from store.ts). */
export function useBootConnections() {
  const loaded = useConnections((s) => s.loaded);
  useEffect(() => { if (!loaded) void useConnections.getState().load(); }, [loaded]);
}

/** Persistent banner under the header while the kill switch or read-only mode is on. */
export function ControlsBanner() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  const setView = useConnections((s) => s.setView);
  if (!controls || !controls.kill_switch_level) return null;
  if (controls.kill_switch_level === "none" && controls.mode !== "read_only") return null;
  const level = controls.kill_switch_level;
  const text = level !== "none" ? t(`ctl.banner.${level}`) : t("ctl.readonly.banner");
  return (
    <div data-testid="ctl-banner" data-level={level !== "none" ? level : "read_only"} role="status"
      className={`flex shrink-0 items-center justify-center gap-3 px-4 py-1.5 text-xs font-semibold text-white ${level !== "none" ? "bg-err" : "bg-warn"}`}>
      <span>{text}</span>
      <button type="button" onClick={() => setView("security")} className="rounded-md border border-white/70 px-3 py-0.5 text-[11px] hover:bg-white/15">{t("ctl.banner.open")}</button>
    </div>
  );
}

/** Full-area overlay for the Connections and Security screens (rendered inside <main>). */
export function ControlsOverlay() {
  const { t } = useT();
  const view = useConnections((s) => s.view);
  const setView = useConnections((s) => s.setView);
  if (!view) return null;
  return (
    <div data-testid={view === "connections" ? "conn-screen" : "ctl-screen"} className="absolute inset-0 z-50 overflow-hidden bg-bg">
      <button type="button" data-testid="conn-close" onClick={() => setView(null)} className="absolute right-4 top-2 z-10 inline-flex items-center gap-1 rounded-md border border-line bg-panel px-2.5 py-1 text-xs font-medium text-ink2 hover:bg-panel2 hover:text-ink">{t("common.close")}</button>
      <div className="h-full pt-9">{view === "connections" ? <ConnectionsView /> : <SecurityCenter />}</div>
    </div>
  );
}
