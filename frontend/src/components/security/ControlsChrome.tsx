"use client";
import { useEffect } from "react";
import { useT } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { go } from "@/lib/navBridge";

/** Loads connections/controls state once (also refreshed by WS frames routed from store.ts). */
export function useBootConnections() {
  const loaded = useConnections((s) => s.loaded);
  useEffect(() => { if (!loaded) void useConnections.getState().load(); }, [loaded]);
}

/** Persistent banner under the header while the kill switch or read-only mode is on. */
export function ControlsBanner() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  if (!controls || !controls.kill_switch_level) return null;
  if (controls.kill_switch_level === "none" && controls.mode !== "read_only") return null;
  const level = controls.kill_switch_level;
  const text = level !== "none" ? t(`ctl.banner.${level}`) : t("ctl.readonly.banner");
  return (
    <div data-testid="ctl-banner" data-level={level !== "none" ? level : "read_only"} role="status"
      className={`flex shrink-0 items-center justify-center gap-3 px-4 py-1.5 text-xs font-semibold text-white ${level !== "none" ? "bg-err" : "bg-warn"}`}>
      <span>{text}</span>
      <button type="button" onClick={() => go("/settings?s=security")} className="rounded-md border border-white/70 px-3 py-0.5 text-[11px] hover:bg-white/15">{t("ctl.banner.open")}</button>
    </div>
  );
}

/** Full-area overlay for the Connections and Security screens (rendered inside <main>). */
