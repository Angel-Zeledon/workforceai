"use client";
import { useEffect } from "react";
import { advance, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { create } from "zustand";
import { useOfficeLayout } from "@/lib/officeLayout";
import { IDLE_AFTER, IDLE_FPS, QUALITY_TIERS, monitorStart, monitorStep, shouldDraw, tierDpr, type MonitorState } from "./officeQuality";

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
interface QualityStore { tier: number; fps: number; pinned: number | null }
export const useQuality = create<QualityStore>(() => ({ tier: 0, fps: 60, pinned: null }));
export const useQualityTier = () => useQuality((s) => s.pinned ?? s.tier);

/** Meshes whose shadow a low tier switched off (their own castShadow, to restore). */
const casters = new WeakMap<THREE.Mesh, boolean>();

const SHADOW_TYPE = { soft: THREE.PCFSoftShadowMap, basic: THREE.PCFShadowMap, off: THREE.PCFShadowMap } as const;

export function FrameDriver() {
  const get = useThree((s) => s.get);
  const gl = useThree((s) => s.gl);
  const setDpr = useThree((s) => s.setDpr);
  const tier = useQualityTier();

  // the tier's pixel ratio and shadows
  useEffect(() => {
    const q = QUALITY_TIERS[tier];
    setDpr(tierDpr(tier, window.devicePixelRatio));
    const { scene } = get();
    const type = SHADOW_TYPE[q.shadows];
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
  }, [tier, gl, get, setDpr]);

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
    let raf = 0, last = 0, frame = 0, busy = 0;
    let onScreen = true, lastInput = performance.now();
    const mon: MonitorState = monitorStart(useQuality.getState().tier);
    const io = new IntersectionObserver(([e]) => { onScreen = e.isIntersecting; }, { threshold: 0 });
    io.observe(gl.domElement);
    const input = () => { lastInput = performance.now(); };
    const evs = ["pointermove", "pointerdown", "wheel", "keydown", "touchstart"] as const;
    for (const e of evs) window.addEventListener(e, input, { passive: true });
    const tick = (now: number) => {
      raf = requestAnimationFrame(tick);
      if (document.hidden || !onScreen) { last = 0; return; }
      const ed = useOfficeLayout.getState();
      const idle = (now - lastInput) / 1000 > IDLE_AFTER && !ed.editing && !ed.dragging;
      const since = last ? (now - last) / 1000 : 1;
      if (!shouldDraw(since, idle)) return;
      const q = QUALITY_TIERS[useQuality.getState().pinned ?? mon.tier];
      if (q.shadowEvery > 1 && frame % q.shadowEvery === 0) gl.shadowMap.needsUpdate = true;
      frame++;
      const t0 = performance.now();
      advance(now / 1000, true, get());
      busy += performance.now() - t0;
      if (last) {
        monitorStep(mon, since, idle ? IDLE_FPS : 60);
        const st = useQuality.getState();
        if (st.tier !== mon.tier) useQuality.setState({ tier: mon.tier });
        if (frame % 30 === 0) useQuality.setState({ fps: Math.round(mon.fps) });
      }
      last = now;
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
