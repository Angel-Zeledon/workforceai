"use client";
// The "Plano" office style: the real office (layout, furniture, agents) in illustrated isometric, drawn with PixiJS.
// Same props as the 3D OfficeScene (none): the office view mounts it with dynamic(..., { ssr: false }) when the style is "flat".
import { useEffect, useRef } from "react";
import { useCost } from "@/lib/cost";
import { useOfficeLayout } from "@/lib/officeLayout";
import { useRewards } from "@/lib/rewards";
import { startProjectsRealtime } from "@/lib/projects/store";
import { useT } from "@/lib/i18n";
import { paceWait } from "../officeQuality";
import { mountFlatScene, type FlatHandle, type Tr } from "./flatScene";

export default function OfficeFlatScene() {
  const host = useRef<HTMLDivElement>(null);
  const { t } = useT();
  const trRef = useRef<Tr>(t as Tr);
  trRef.current = t as Tr;

  // the same data feeds as the 3D scene: project summaries, the organization's layout and catalog, remaining budget per agent
  useEffect(() => startProjectsRealtime(), []);
  useEffect(() => { void useRewards.getState().load(); void useOfficeLayout.getState().load(); }, []);
  useEffect(() => {
    const { loadStatus } = useCost.getState();
    void loadStatus();
    const i = setInterval(() => void useCost.getState().loadStatus(), 20000);
    return () => clearInterval(i);
  }, []);

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    let disposed = false;
    let handle: FlatHandle | null = null;
    let tabVisible = typeof document === "undefined" ? true : !document.hidden;
    let onScreen = true;
    let resting = false;
    const sync = () => handle?.setRunning(tabVisible && onScreen && !resting);
    // Pacing (same rule as the 3D office, officeQuality.paceWait): on a computer without a GPU every Pixi frame holds the page for
    // hundreds of ms; after a slow stretch the scene rests as long again so clicks and closing the tab always get through.
    let pace = 0, prev = 0, restTimer: ReturnType<typeof setTimeout> | undefined;
    const watch = (t: number) => {
      pace = requestAnimationFrame(watch);
      const wait = prev && handle && !resting ? paceWait(t - prev) : 0;
      prev = t;
      if (wait > 0) {
        resting = true; prev = 0; sync();
        restTimer = setTimeout(() => { resting = false; prev = 0; sync(); }, wait);
      }
    };
    pace = requestAnimationFrame(watch);
    mountFlatScene(el, () => trRef.current).then((h) => {
      if (disposed) { h.destroy(); return; }
      handle = h;
      sync();
    }).catch((e) => { console.error("FLAT mount failed", e); });
    // paused while the tab is hidden or the scene is scrolled out of view: no frames, no heat
    const onVis = () => { tabVisible = !document.hidden; sync(); };
    document.addEventListener("visibilitychange", onVis);
    const io = typeof IntersectionObserver !== "undefined" ? new IntersectionObserver((e) => { onScreen = e[e.length - 1]?.isIntersecting ?? true; sync(); }) : null;
    io?.observe(el);
    return () => {
      disposed = true;
      cancelAnimationFrame(pace); clearTimeout(restTimer);
      document.removeEventListener("visibilitychange", onVis);
      io?.disconnect();
      handle?.destroy();
      handle = null;
    };
  }, []);

  return <div ref={host} data-testid="office-flat" className="absolute inset-0 overflow-hidden bg-white" />;
}
