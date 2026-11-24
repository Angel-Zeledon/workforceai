"use client";
import { useCurrentOfficeStore } from "@/lib/currentOffice";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { useOfficeStyle, useOfficeLayout } from "@/lib/officeLayout";
import { usePreferences } from "@/lib/preferences";
import { Bloom, EffectComposer, N8AO, SMAA, TiltShift2, ToneMapping } from "@react-three/postprocessing";
import { ToneMappingMode } from "postprocessing";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { APPEAR_SECONDS, appearPose, lookOf } from "./officeTheme";
import { StudBatch, themeTree } from "./themeRuntime";
import { QUALITY_TIERS } from "./officeQuality";
import { useQualityTier } from "./FrameDriver";

/** Console handles for screenshots and FPS checks: development builds, or a build made with NEXT_PUBLIC_OFFICE_DEBUG=true. */
export const OFFICE_DEBUG = process.env.NODE_ENV !== "production" || process.env.NEXT_PUBLIC_OFFICE_DEBUG === "true";

/** Frames between two sweeps of the scene (new meshes pick up the style within this many frames). */
const SWEEP_EVERY = 8;

/**
 * Applies the office style to everything in the scene (materials, geometry, studs) and keeps the edge-glow clock running.
 * In "classic" it restores whatever was styled and then stays idle.
 */
export function ThemeApplier() {
  const style = useOfficeStyle();
  const scene = useThree((s) => s.scene);
  const getState = useThree((s) => s.get);
  const batch = useMemo(() => new StudBatch(), []);
  const st = useRef({ frame: 0, applied: "classic", idleSweeps: 0 });
  useEffect(() => () => batch.dispose(), [batch]);
  // development only: switch styles and inspect the scene from the console (screenshots, FPS checks)
  useEffect(() => {
    if (!OFFICE_DEBUG) return;
    const w = window as unknown as { __officeStyle?: unknown };
    w.__officeStyle = {
      scene,
      /** the live camera (to project a point of the scene to the screen in browser checks) */
      camera: () => getState().camera,
      /** close-ups for screenshots: narrows the lens on a point of the floor plan (fov null: back to the default view) */
      view: (fov: number | null, x = 0.5, y = 0.4, z = -0.3) => {
        const st = getState();
        const cam = st.camera as THREE.PerspectiveCamera;
        const c = st.controls as unknown as { target: THREE.Vector3; update: () => void } | null;
        cam.fov = fov ?? 32; cam.updateProjectionMatrix();
        if (c) { c.target.set(x, y, z); c.update(); }
      },
      set: (s: string) => useOfficeLayout.setState((st) => ({ layout: { ...st.layout, style: s === "classic" ? undefined : (s as never) } })),
    };
    return () => { delete w.__officeStyle; };
  }, [scene, getState]);
  useFrame(() => {
    const s = st.current;
    s.frame++;
    const changed = s.applied !== style;
    if (style === "classic" && !changed && s.idleSweeps > 2) return; // nothing left to restore
    if (changed || s.frame % SWEEP_EVERY === 0) {
      const r = themeTree(scene, style, true);
      batch.set(r.studs);
      s.applied = style;
      s.idleSweeps = style === "classic" ? s.idleSweeps + 1 : 0;
    }
    if (lookOf(style).studs) batch.update();
  });
  return <primitive object={batch.mesh} />;
}

// ------------------------------------------------------------------------------------------------ entrances

/**
 * A piece's entrance in the active style (bricks stack in, paper unfolds, neon strikes, ...): played when the piece is
 * placed while editing and, staggered, when the style changes. Nothing in "classic" or with reduced motion.
 */
export function Appear({ seed = 0, leaving = false, children }: { seed?: number; leaving?: boolean; children: React.ReactNode }) {
  const style = useOfficeStyle();
  const ref = useRef<THREE.Group>(null);
  const anim = useRef({ t: leaving ? 0 : -1, delay: 0 });
  const first = useRef(true);
  useEffect(() => {
    if (leaving) return;
    const placedNow = first.current && useOfficeLayout.getState().editing;
    if (!first.current || placedNow) anim.current = { t: 0, delay: placedNow ? 0 : (seed % 12) * 0.05 };
    first.current = false;
  }, [style, seed, leaving]);
  useFrame((_, dt) => {
    const a = anim.current, g = ref.current;
    if (a.t < 0 || !g) return;
    const kind = lookOf(style).appear;
    const dur = APPEAR_SECONDS[kind];
    if (!dur || usePreferences.getState().prefs.reduceMotion) a.t = Infinity;
    else if (a.delay > 0) a.delay -= dt;
    else a.t += dt / dur;
    // leaving plays the same entrance backwards (bricks come apart, paper folds down, neon goes out, ...)
    const p = leaving ? appearPose(kind, Math.min(0.999, Math.max(0, 1 - a.t))) : appearPose(kind, a.t);
    g.position.y = p.y; g.scale.set(p.sx, p.sy, p.sz); g.rotation.set(p.rx, p.ry, 0); g.visible = leaving && a.t >= 1 ? false : p.visible;
    if (a.t >= 1) a.t = -1;
  });
  return <group ref={ref}>{children}</group>;
}

/**
 * Pieces removed from the layout stay a moment to play their exit in the active style (none in classic or with reduced
 * motion). Returns the removed pieces still on their way out.
 */
export function useLeaving<T extends { uid: string }>(items: T[]): T[] {
  const style = useOfficeStyle();
  const prev = useRef<T[]>(items);
  const [leaving, setLeaving] = useState<T[]>([]);
  useEffect(() => {
    const now = new Set(items.map((i) => i.uid));
    const gone = prev.current.filter((i) => !now.has(i.uid));
    prev.current = items;
    const dur = APPEAR_SECONDS[lookOf(style).appear];
    if (!gone.length || !dur || usePreferences.getState().prefs.reduceMotion) return;
    const out = (l: T[]) => l.filter((x) => !gone.some((g) => g.uid === x.uid));
    setLeaving((l) => [...out(l), ...gone]);
    const tm = setTimeout(() => setLeaving(out), dur * 1000 + 80);
    return () => clearTimeout(tm);
  }, [items, style]);
  return leaving;
}

/** Small stable number from a string (stagger of the entrances). */
export function seedOf(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

// ------------------------------------------------------------------------------------------------ ambient effects

/**
 * What a style adds to the scene besides restyling it: the post-processing (ambient occlusion, tilt-shift, antialiasing)
 * of Maqueta. Nothing in "classic": no composer, no extra pass, no download.
 */
export function StyleAmbient() {
  const style = useOfficeStyle();
  const look = lookOf(style);
  return <>{look.post && <StylePost style={style} />}</>;
}

// ------------------------------------------------------------------------------------------------ post-processing

/**
 * The style's post-processing: one composer, mounted only in the styles that have one. The scene is drawn once into a
 * half-float buffer, then: ambient occlusion (N8AO), ink, bloom of what glows, ACES tone mapping (the composer turns
 * the renderer's own off, so this keeps every color exactly as in classic), SMAA and tilt-shift. The quality tier
 * (FrameDriver) drops the costly ones on slow computers.
 */
/**
 * N8AO turns "transparency aware" on by itself when the scene has any transparent material (the office always does:
 * glass, labels, rings), and then draws the whole scene twice more every frame, ~15 % of the frame on a laptop. The AO
 * only needs the opaque depth: switched off.
 */
function opaqueAo(pass: { autoDetectTransparency?: boolean; configuration: { transparencyAware: boolean } } | null) {
  if (!pass) return;
  pass.autoDetectTransparency = false;
  pass.configuration.transparencyAware = false;
}

function StylePost({ style }: { style: OfficeStyle }) {
  const look = lookOf(style);
  const post = look.post!;
  const q = QUALITY_TIERS[useQualityTier()];
  const ao = post.ao > 0 && q.ao !== "off";
  const bloom = post.bloom > 0 && q.bloom;
  const tilt = post.tilt > 0 && q.tilt;
  return (
    <EffectComposer multisampling={0} depthBuffer>
      {ao ? <N8AO ref={opaqueAo} halfRes quality={q.ao === "full" ? "medium" : "performance"} aoRadius={post.aoRadius} intensity={post.ao} distanceFalloff={0.7} depthAwareUpsampling /> : <></>}
      {bloom ? <Bloom mipmapBlur intensity={post.bloom} luminanceThreshold={1.05} luminanceSmoothing={0.25} /> : <></>}
      <ToneMapping mode={ToneMappingMode.ACES_FILMIC} />
      {post.smaa ? <SMAA /> : <></>}
      {tilt ? <TiltShift2 blur={0.16 * post.tilt} taper={0.55} start={[0, 0.5]} end={[1, 0.5]} samples={6} /> : <></>}
    </EffectComposer>
  );
}

/**
 * The switch between two styles: a short veil in the new style's colors covers the frame while the scene restyles and
 * rebuilds (shaders compile, pieces play their entrance behind it). DOM overlay, placed over the canvas.
 */
export function StyleVeil() {
  const style = useOfficeStyle();
  const [veil, setVeil] = useState<{ key: number; bg: string } | null>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const reduce = usePreferences.getState().prefs.reduceMotion || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduce) return;
    const [x, y, z] = lookOf(style).swatch;
    setVeil({ key: Date.now(), bg: `radial-gradient(circle at 50% 55%, ${x} 0%, ${y} 60%, ${z} 100%)` });
    const t = setTimeout(() => setVeil(null), 700);
    return () => clearTimeout(t);
  }, [style]);
  if (!veil) return null;
  return (
    <div key={veil.key} aria-hidden className="pointer-events-none absolute inset-0 z-[5]" style={{ background: veil.bg, animation: "office-veil .7s ease-in-out forwards" }}>
      <style>{"@keyframes office-veil{0%{opacity:0}30%{opacity:.8}100%{opacity:0}}"}</style>
    </div>
  );
}

/** Short fade while the office on screen changes (the layout and its people swap behind it). No motion with reduced motion. */
export function OfficeFade() {
  const current = useCurrentOfficeStore((s) => s.stored);
  const [on, setOn] = useState(false);
  const last = useRef(current);
  useEffect(() => {
    if (last.current === current) return;
    last.current = current;
    const reduce = usePreferences.getState().prefs.reduceMotion || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduce) return;
    setOn(true);
    const t = setTimeout(() => setOn(false), 80);
    return () => clearTimeout(t);
  }, [current]);
  return <div aria-hidden data-testid="office-fade" className={`pointer-events-none absolute inset-0 z-[6] bg-bg ${on ? "opacity-100" : "opacity-0 transition-opacity duration-300 ease-out"}`} />;
}
