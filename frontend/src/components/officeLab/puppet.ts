// EXPERIMENTAL (spike /office-lab, desechable): poses clave de los muñecos de los paneles C (maqueta stop-motion) y D
// (tinta / cel). Puro: a partir del fotograma del guion da los ángulos de cada articulación. Cada panel decide CÓMO llega
// a esa pose: C a saltos de 12 fps (stop-motion), D con muelles (sobreimpulso y seguimiento).
// Convención (como gestures.ts): el muñeco mira a +z; rotación x negativa = la extremidad va hacia delante.
import { clamp01, smooth, type Frame } from "./script";

export const JOINTS = [
  "y", "pelvis", "torsoX", "torsoY", "torsoZ", "headX", "headY", "headZ",
  "lShX", "lShZ", "lElX", "rShX", "rShZ", "rElX", "lHipX", "rHipX", "lKneeX", "rKneeX",
] as const;
export type Joint = (typeof JOINTS)[number];
export type PPose = Record<Joint, number>;
export type Role = "agent" | "partner" | "boss";

/** Altura de la pelvis de pie / sentado (m): piernas de 0.45 + 0.45. */
export const PELVIS_STAND = 0.93;
export const PELVIS_SEAT = 0.5;

export const zero = (): PPose => {
  const p = {} as PPose;
  for (const k of JOINTS) p[k] = 0;
  p.pelvis = PELVIS_STAND;
  return p;
};

function rest(p: PPose, t: number, ph: number) {
  p.lShX = -0.08; p.rShX = -0.08; p.lShZ = 0.08; p.rShZ = 0.08; p.lElX = -0.2; p.rElX = -0.2;
  p.torsoX = Math.sin(t * 1.6 + ph) * 0.015; p.headY = Math.sin(t * 0.5 + ph) * 0.12;
}
function seated(p: PPose, k: number) {
  p.pelvis = PELVIS_STAND + (PELVIS_SEAT - PELVIS_STAND) * k;
  p.lHipX = -1.5 * k; p.rHipX = -1.5 * k; p.lKneeX = 1.5 * k; p.rKneeX = 1.5 * k;
}
function typing(p: PPose, t: number, amt: number) {
  p.lShX += (-1.05 - p.lShX) * amt; p.rShX += (-1.05 - p.rShX) * amt;
  p.lElX += (-0.75 - p.lElX) * amt; p.rElX += (-0.75 - p.rElX) * amt;
  p.lShZ = 0.12; p.rShZ = 0.12;
  p.lElX += Math.sin(t * 17) * 0.1 * amt; p.rElX += Math.sin(t * 17 + 2.1) * 0.1 * amt;
  p.headX = 0.14 * amt + Math.sin(t * 2.3) * 0.03; p.torsoX = 0.1 * amt;
}
function talking(p: PPose, t: number, amt: number) {
  p.rShX = -0.95 + Math.sin(t * 2.6) * 0.4 * amt; p.rShZ = 0.28; p.rElX = -1.05 + Math.sin(t * 3.4) * 0.45 * amt;
  p.lShX = -0.45 + Math.sin(t * 2.1 + 1) * 0.2 * amt; p.lElX = -0.8;
  p.headX = Math.sin(t * 4.2) * 0.06; p.headY = Math.sin(t * 1.2) * 0.15; p.torsoY = Math.sin(t * 1.2) * 0.12;
}
const nod = (p: PPose, t: number, amt: number) => { p.headX += Math.max(0, Math.sin(t * 6)) * 0.22 * amt; };

/** La pose objetivo de un personaje en el instante `t` del guion. */
export function poseFor(role: Role, f: Frame, t: number): PPose {
  const p = zero();
  const ph = role === "agent" ? 0 : role === "partner" ? 2.1 : 4.2;
  rest(p, t, ph);
  if (role === "partner") {
    const m = f.partner.mode;
    if (m === "talk") talking(p, t, 1);
    else if (m === "listen") nod(p, t, 1);
    else if (m === "hand") { p.rShX = -1.35; p.rElX = -0.15; p.torsoX = 0.1; p.headX = 0.15; }
    return p;
  }
  if (role === "boss") {
    seated(p, 1);
    const m = f.boss.mode, u = f.boss.u;
    if (m === "work") typing(p, t, 1);
    else if (m === "look") { p.headX = -0.1; p.headY = 0; p.lShX = -0.7; p.rShX = -0.7; p.lElX = -0.9; p.rElX = -0.9; }
    else if (m === "receive") { const r = Math.sin(Math.PI * clamp01(u)); p.rShX = -0.6 - 0.8 * r; p.rElX = -0.6 + 0.4 * r; p.torsoX = 0.15 * r; }
    else if (m === "approve") { nod(p, t, 1.3); p.lShX = -0.7; p.rShX = -0.7; p.lElX = -1.0; p.rElX = -1.0; }
    else if (m === "clap") {
      const c = Math.abs(Math.sin(t * 11));
      p.lShX = -1.15; p.rShX = -1.15; p.lElX = -0.9; p.rElX = -0.9; p.lShZ = -0.25 - 0.25 * c; p.rShZ = -0.25 - 0.25 * c; p.headX = -0.08;
    }
    return p;
  }
  // el agente
  if (f.walking) {
    const s = Math.sin(f.stride * 3.3), amp = 0.3 + 0.7 * f.speed;
    p.y = Math.abs(Math.cos(f.stride * 3.3)) * 0.035 * amp;
    p.lHipX = s * 0.6 * amp; p.rHipX = -s * 0.6 * amp;
    p.lKneeX = Math.max(0, Math.sin(f.stride * 3.3 + 1.3)) * 1.0 * amp; p.rKneeX = Math.max(0, -Math.sin(f.stride * 3.3 + 1.3)) * 1.0 * amp;
    p.lShX = -s * 0.5 * amp; p.rShX = s * 0.5 * amp; p.lElX = -0.35; p.rElX = -0.35;
    p.torsoX = 0.07 * amp; p.torsoY = -s * 0.12 * amp;
    if (f.folder === "agent") { p.rShX = -0.75; p.rElX = -1.1; p.rShZ = 0.15; }
    return p;
  }
  if (f.seated > 0) {
    seated(p, smooth(f.seated));
    if (f.typing > 0) typing(p, t, f.typing);
    else { p.lShX = -0.6; p.rShX = -0.6; p.lElX = -0.9; p.rElX = -0.9; }
    return p;
  }
  switch (f.phase) {
    case "talk":
      if (f.bubble?.who === "agent") talking(p, t, 1);
      else if (f.bubble?.who === "partner") nod(p, t, 1);
      if (f.folder === "agent") { p.rShX = -0.75; p.rElX = -1.1; }
      break;
    case "give": p.rShX = -0.4 - 1.05 * f.reach; p.rElX = -0.9 + 0.75 * f.reach; p.torsoX = 0.14 * f.reach; p.headX = 0.12 * f.reach; break;
    case "approve": p.headX = -0.06; p.lShX = -0.3; p.lElX = -0.6; break;
    case "celebrate": {
      const u = f.local;
      const crouch = Math.sin(clamp01(u / 0.25) * Math.PI) + 0.8 * Math.sin(clamp01((u - 0.62) / 0.2) * Math.PI);
      const air = Math.sin(clamp01((u - 0.25) / 0.37) * Math.PI);
      p.y = air * 0.32;
      p.pelvis -= crouch * 0.14;
      p.lHipX = -crouch * 0.6 - air * 0.3; p.rHipX = -crouch * 0.6 - air * 0.5; p.lKneeX = crouch * 1.1 + air * 0.5; p.rKneeX = crouch * 1.1 + air * 0.8;
      p.torsoX = crouch * 0.2 - air * 0.12;
      const up = smooth(clamp01((u - 0.2) / 0.15));
      const pump = u > 0.85 ? Math.sin((u - 0.85) * 13) * 0.35 : 0;
      p.rShX = -0.1 + (-2.75 - -0.1) * up + pump; p.rShZ = 0.2 * up; p.rElX = -0.2 - 0.3 * up;
      const lUp = up * (1 - smooth(clamp01((u - 0.9) / 0.3)));
      p.lShX = -0.1 + (-2.6 - -0.1) * lUp; p.lShZ = 0.35 * lUp; p.lElX = -0.2;
      p.headX = -0.15 * up;
      const out = smooth(clamp01((u - 1.45) / 0.35));
      for (const k of ["rShX", "lShX", "rElX", "headX"] as const) p[k] *= 1 - out;
      break;
    }
  }
  return p;
}
