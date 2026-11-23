"use client";
import { useEffect } from "react";
import { advance, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { create } from "zustand";
import { useOfficeLayout, useOfficeStyle } from "@/lib/officeLayout";
import { lookOf } from "./officeTheme";
import { IDLE_AFTER, QUALITY_TIERS, isSoftwareRenderer, monitorStart, monitorStep, paceWait, shouldDraw, startingTier, targetFps, tierDpr, type MonitorState } from "./officeQuality";

/**
 * Drives the office canvas (docs/architecture/office-editor.md, "Styles" > "Speed on slow computers"). The Canvas runs
 * with frameloop="never" and this loop decides when a frame is drawn:
 *  - never while the tab is hidden or the canvas is off screen (another page, scrolled away);
 *  - at full rate while someone uses the office (pointer, wheel, keys, the editor) and at IDLE_FPS otherwise (the
 *    characters keep moving, at half rate);
 *  - it measures the frames it draws and steps the quality tier (officeQuality.ts) down when they are too slow and
 *    back up when there is room, with hysteresis. The tier caps the pixel ratio, the shadows and, through
 *    `useQualityTier`, the post-processing of the active style.
 */
interface QualityStore { tier: number; fps: number; pinned: number | null; /** the tier never climbed above (software rendering) */ floor: number; /** the GL renderer string, once read */ renderer: string }
export const useQuality = create<QualityStore>(() => ({ tier: 0, fps: 60, pinned: null, floor: 0, renderer: "" }));
export const useQualityTier = () => useQuality((s) => s.pinned ?? s.tier);

/** Meshes whose shadow a low tier switched off (their own castShadow, to restore). */
const casters = new WeakMap<THREE.Mesh, boolean>();

const SHADOW_TYPE = { soft: THREE.PCFSoftShadowMap, basic: THREE.PCFShadowMap, off: THREE.PCFShadowMap } as const;
/** Pixel draws hard, unfiltered shadows. */
const HARD_SHADOW = THREE.BasicShadowMap;

/** The GPU driver's own name for itself (WEBGL_debug_renderer_info; falls back to the generic one). */
export function softwareName(ctx: WebGLRenderingContext | WebGL2RenderingContext): string {
  try {
    const ext = ctx.getExtension("WEBGL_debug_renderer_info");
    return String(ctx.getParameter(ext ? ext.UNMASKED_RENDERER_WEBGL : ctx.RENDERER) || "");
  } catch { return ""; }
}

/** Reads the machine once, before the first frame: no GPU -> the lowest tier and it stays there; a weak machine -> tier 2. */
function detectStart(gl: THREE.WebGLRenderer) {
  const st = useQuality.getState();
  if (st.renderer) return;
  // development only: localStorage office-nodetect=1 skips the detection (to measure what a machine without a GPU does at full quality)
  if (process.env.NODE_ENV !== "production" && localStorage.getItem("office-nodetect") === "1") { useQuality.setState({ renderer: "skipped" }); return; }
  const renderer = softwareName(gl.getContext());
  const nav = navigator as Navigator & { deviceMemory?: number };
  const { tier, floor } = startingTier({ renderer, memoryGb: nav.deviceMemory, cores: navigator.hardwareConcurrency });
  useQuality.setState({ renderer: renderer || "unknown", floor, tier: Math.max(st.tier, tier) });
  if (isSoftwareRenderer(renderer)) console.info("[office] software rendering detected (" + renderer + "): drawing at the lowest quality tier");
}

export function FrameDriver() {
  const get = useThree((s) => s.get);
  const gl = useThree((s) => s.gl);
  detectStart(gl); // synchronous, before the first effect and the first frame
  const setDpr = useThree((s) => s.setDpr);
  const tier = useQualityTier();
  const pixel = !!lookOf(useOfficeStyle()).pixel;

  // the tier's pixel ratio and shadows (Pixel keeps the screen's own ratio: its scale must stay a whole number of physical pixels)
  useEffect(() => {
    const q = QUALITY_TIERS[tier];
    setDpr(pixel ? window.devicePixelRatio || 1 : tierDpr(tier, window.devicePixelRatio));
    const { scene } = get();
    const type = pixel && q.shadows !== "off" ? HARD_SHADOW : SHADOW_TYPE[q.shadows];
    const typeChanged = gl.shadowMap.type !== type;
    gl.shadowMap.type = type;
    gl.shadowMap.autoUpdate = q.shadowEvery <= 1;
    gl.shadowMap.needsUpdate = true;
    scene.traverse((o) => {
      const l = o as THREE.DirectionalLight;
      if (l.isDirectionalLight) {
        const ud = l.userData as { castShadow0?: boolean };
        if (ud.castShadow0 === undefined) ud.castShadow0 = l.castShadow;
        l.castShadow = ud.castShadow0 && q.shadows !== "off";
        if (l.castShadow && l.shadow.mapSize.x !== q.shadowMap) {
          l.shadow.mapSize.set(q.shadowMap, q.shadowMap);
          l.shadow.map?.dispose();
          l.shadow.map = null;
        }
      }
      // a new shadow filter is a new shader: every lit material recompiles once
      if (typeChanged) {
        const mats = (o as THREE.Mesh).material;
        if (mats) for (const m of Array.isArray(mats) ? mats : [mats]) m.needsUpdate = true;
      }
    });
  }, [tier, pixel, gl, get, setDpr]);

  // small pieces stop casting shadows on the lower tiers (fewer draw calls in the shadow pass); re-checked every
  // 1.5 s so pieces added later follow, and restored exactly when the tier allows them again
  useEffect(() => {
    const min = QUALITY_TIERS[tier].minCaster;
    const sphere = new THREE.Sphere();
    const apply = () => {
      get().scene.traverse((o) => {
        const m = o as THREE.Mesh;
        if (!m.isMesh) return;
        const was = casters.get(m);
        if (!min) { if (was !== undefined) { m.castShadow = was; casters.delete(m); } return; }
        if (was === undefined && !m.castShadow) return;
        if (!m.geometry.boundingSphere) m.geometry.computeBoundingSphere();
        sphere.copy(m.geometry.boundingSphere!).applyMatrix4(m.matrixWorld);
        const small = sphere.radius < min;
        if (small && was === undefined) { casters.set(m, true); m.castShadow = false; }
        else if (!small && was !== undefined) { m.castShadow = was; casters.delete(m); }
      });
    };
    apply();
    if (!min) return;
    const iv = setInterval(apply, 1500);
    return () => clearInterval(iv);
  }, [tier, get]);

  useEffect(() => {
    let raf = 0, last = 0, frame = 0, busy = 0, drawnAt = 0, restUntil = 0, frozen = false;
    let onScreen = true, lastInput = performance.now();
    const mon: MonitorState = monitorStart(useQuality.getState().tier, useQuality.getState().floor);
    const io = new IntersectionObserver(([e]) => { onScreen = e.isIntersecting; }, { threshold: 0 });
    io.observe(gl.domElement);
    const input = () => { lastInput = performance.now(); };
    const evs = ["pointermove", "pointerdown", "wheel", "keydown", "touchstart"] as const;
    for (const e of evs) window.addEventListener(e, input, { passive: true });
    const tick = (now: number) => {
      raf = requestAnimationFrame(tick);
      if (document.hidden || !onScreen || frozen) { last = 0; drawnAt = 0; return; }
      // pacing: how long the previous draw held the page (start of that tick to this one); a slow one is followed by a rest
      const tn = performance.now();
      if (drawnAt) { restUntil = tn + paceWait(tn - drawnAt); drawnAt = 0; }
      if (tn < restUntil) return;
      const ed = useOfficeLayout.getState();
      const idle = (now - lastInput) / 1000 > IDLE_AFTER && !ed.editing && !ed.dragging;
      const since = last ? (now - last) / 1000 : 1;
      const tierNow = useQuality.getState().pinned ?? mon.tier;
      if (!shouldDraw(since, idle, tierNow)) return;
      const q = QUALITY_TIERS[tierNow];
      if (q.shadowEvery > 1 && frame % q.shadowEvery === 0) gl.shadowMap.needsUpdate = true;
      frame++;
      const t0 = performance.now();
      advance(now / 1000, true, get());
      busy += performance.now() - t0;
      if (last) {
        monitorStep(mon, since, targetFps(mon.tier, idle), !idle);
        const st = useQuality.getState();
        if (st.tier !== mon.tier) useQuality.setState({ tier: mon.tier });
        if (frame % 30 === 0) useQuality.setState({ fps: Math.round(mon.fps) });
      }
      last = now;
      drawnAt = tn;
    };
    raf = requestAnimationFrame(tick);
    // development (or NEXT_PUBLIC_OFFICE_DEBUG) only: read / pin the quality tier from the console (screenshots, FPS checks)
    const w = window as unknown as { __officeQuality?: unknown };
    if (process.env.NODE_ENV !== "production" || process.env.NEXT_PUBLIC_OFFICE_DEBUG === "true") {
      w.__officeQuality = {
        get: () => ({ tier: useQuality.getState().pinned ?? mon.tier, fps: Math.round(mon.fps), pinned: useQuality.getState().pinned }),
        /** drawn frames and main-thread milliseconds spent drawing them, since the office opened */
        counters: () => ({ frames: frame, busyMs: busy }),
        pin: (t: number | null) => useQuality.setState({ pinned: t }),
        /** stops the real loop and draws `n` frames of virtual time `dt` seconds apart (deterministic walks for screenshots) */
        freeze: (on: boolean) => { frozen = on; },
        step: (n: number, dt: number, draw = true) => {
          const sc = get().scene; let t = get().clock.elapsedTime;
          for (let i = 0; i < n; i++) { t += dt; sc.visible = draw && i === n - 1; advance(t, true, get()); } // only the last frame is drawn (the rest just move the people)
          sc.visible = true;
        },
        /** page pixel (client coordinates) of the floor point (x, z) at height y: drives the editor with real pointer events in tests */
        project: (x: number, z: number, y = 0) => {
          const st = get(); const v = new THREE.Vector3(x, y, z).project(st.camera); const r = gl.domElement.getBoundingClientRect();
          return { x: r.left + (v.x + 1) / 2 * r.width, y: r.top + (1 - v.y) / 2 * r.height };
        },
        /** renderer.info of the last frame (draw calls, triangles) and what lives in the GPU (programs, geometries, textures) */
        info: () => ({
          calls: gl.info.render.calls, triangles: gl.info.render.triangles, programs: gl.info.programs?.length ?? 0,
          geometries: gl.info.memory.geometries, textures: gl.info.memory.textures, dpr: gl.getPixelRatio(),
          renderer: softwareName(gl.getContext()),
        }),
      };
    }
    return () => {
      cancelAnimationFrame(raf);
      io.disconnect();
      for (const e of evs) window.removeEventListener(e, input);
      delete w.__officeQuality;
    };
  }, [gl, get]);
  return null;
}
