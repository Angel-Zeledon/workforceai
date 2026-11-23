// Pure parts of the connected people drawn in the "Plano" office (flatPeople.ts): the walk of a figure as a 2D pose, the colour
// of a person, and the floor point under a click. No Pixi / React / app imports: it runs under `node --test`.
import type { Pose2 } from "./isoMath";

/** isoMath.STAND_HIP (a copy: this file imports nothing at run time so `node --test` can load it). */
const STAND_HIP = 30;

/** "hsl(h, s%, l%)" (the form presence/logic.colorFor returns) -> 0xrrggbb. Anything else: the neutral blue-grey. */
export function hslToNum(css: string): number {
  const m = /^hsl\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)%\s*,\s*(\d+(?:\.\d+)?)%\s*\)$/.exec(css.trim());
  if (!m) return 0x6b7a90;
  const h = (+m[1] % 360) / 360, s = +m[2] / 100, l = +m[3] / 100;
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s, p = 2 * l - q;
  const f = (t: number) => {
    t = (t + 1) % 1;
    return t < 1 / 6 ? p + (q - p) * 6 * t : t < 0.5 ? q : t < 2 / 3 ? p + (q - p) * (2 / 3 - t) * 6 : p;
  };
  const b = (v: number) => Math.round(v * 255);
  return (b(f(h + 1 / 3)) << 16) | (b(f(h)) << 8) | b(f(h - 1 / 3));
}

/** Sober clothes: the saturated hash colour of a person is softened toward grey so the figure sits with the agents'. */
export function softenColor(c: number, k = 0.55): number {
  const r = (c >> 16) & 255, g = (c >> 8) & 255, b = c & 255, grey = 0.299 * r + 0.587 * g + 0.114 * b;
  const m = (v: number) => Math.round(grey + (v - grey) * k);
  return (m(r) << 16) | (m(g) << 8) | m(b);
}

/** The side-on pose of a person standing (breathing) or walking at time `t` seconds (legs and arms swing, the hips bob). */
export function personPose(moving: boolean, t: number): Pose2 {
  if (!moving) {
    const b = Math.sin(t * 1.6);
    return { hipY: STAND_HIP + b * 0.4, lean: 0.02, legA: [0.03, -0.03], knee: [0, 0], armA: [0.08 + b * 0.02, 0.05], elbow: [0.18, 0.15], head: 0, sx: 1, sy: 1, seated: false };
  }
  const w = Math.sin(t * 9);
  return {
    hipY: STAND_HIP - Math.abs(Math.cos(t * 9)) * 1.6, lean: 0.1,
    legA: [w * 0.55, -w * 0.55], knee: [Math.max(0, -w) * 0.7, Math.max(0, w) * 0.7],
    armA: [-w * 0.45, w * 0.45], elbow: [0.35, 0.35], head: 0, sx: 1, sy: 1, seated: false,
  };
}

/** Canvas client coordinates -> scene pixels, given the canvas rectangle, the world's offset on the canvas and its scale. */
export function sceneFromClient(clientX: number, clientY: number, rect: { left: number; top: number }, world: { x: number; y: number }, k: number) {
  return { x: (clientX - rect.left - world.x) / k, y: (clientY - rect.top - world.y) / k };
}
