"use client";
// One office style shown as a short silent loop of the SAME scene in all five (an agent walks a folder to the boss and
// celebrates, recorded from the lab at /office-lab). A still poster is always there; the loop (an animated image of
// ~200 KB) is only loaded and shown while the window is playing, so five of them never run at once and no WebGL lives in
// the menu. With reduced motion the poster stays. The Píxel pair is stored enlarged 4x (nearest neighbour) and scaled
// smoothly: every pixel of the art comes out the same size in the window, which a non-integer 'pixelated' scale cannot do.
import { useEffect, useState } from "react";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { usePreferences } from "@/lib/preferences";

export const STYLE_POSTER: Record<OfficeStyle, string> = {
  classic: "/office/styles/classic.jpg", studio: "/office/styles/studio.jpg", flat: "/office/styles/flat.jpg", clay: "/office/styles/clay.jpg", pixel: "/office/styles/pixel.jpg",
};
export const STYLE_LOOP: Record<OfficeStyle, string> = {
  classic: "/office/styles/classic.webp", studio: "/office/styles/studio.webp", flat: "/office/styles/flat.webp", clay: "/office/styles/clay.webp", pixel: "/office/styles/pixel.webp",
};

/** true when the person asked for less movement (the app's own switch or the system's). */
export function useReducedMotion(): boolean {
  const pref = usePreferences((s) => s.prefs.reduceMotion);
  const [os, setOs] = useState(false);
  useEffect(() => {
    const m = window.matchMedia("(prefers-reduced-motion: reduce)");
    const on = () => setOs(m.matches);
    on();
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return !!pref || os;
}

export function StyleClip({ id, playing, className = "", priority = false }: { id: OfficeStyle; playing: boolean; className?: string; priority?: boolean }) {
  const reduced = useReducedMotion();
  const [ready, setReady] = useState(false);
  const run = playing && !reduced;
  useEffect(() => { if (!run) setReady(false); }, [run]);
  return (
    <div className={`relative w-full overflow-hidden bg-panel2 aspect-[16/10] ${className}`}>
      {/* eslint-disable @next/next/no-img-element -- recorded loops, not worth the image optimizer */}
      <img src={STYLE_POSTER[id]} alt="" width={448} height={280} draggable={false} loading={priority ? "eager" : "lazy"} className="absolute inset-0 h-full w-full object-cover" />
      {run && <img src={STYLE_LOOP[id]} alt="" width={448} height={280} draggable={false} onLoad={() => setReady(true)} className={`absolute inset-0 h-full w-full object-cover transition-opacity duration-200 ${ready ? "opacity-100" : "opacity-0"}`} />}
      {/* eslint-enable @next/next/no-img-element */}
    </div>
  );
}
