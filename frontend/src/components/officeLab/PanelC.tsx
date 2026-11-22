// EXPERIMENTAL (spike /office-lab, desechable).
// Panel C, "Plano": PixiJS v8 en 2D isométrico. Nada de 3D: el plano se proyecta a mano (x, z) -> pantalla, los
// muebles son polígonos vectoriales (Graphics) con trazo de ilustración, y los personajes son figuras de perfil 3/4
// dibujadas cada fotograma con interpolación de ángulos y squash & stretch (rebote al pisar, anticipación y aterrizaje
// en el salto). Mismo guion que los demás paneles.
"use client";
import { useEffect, useRef } from "react";
import type * as PIXI from "pixi.js";
import { labTime } from "./clock";
import { PLAN, clamp01, sample, smooth, type Frame, type V2 } from "./script";

const S = 40; // px por metro antes de ajustar al lienzo
const CS = Math.cos(Math.PI / 6);
const INK = 0x1f2a37;
const ACCENT = 0x0a66c2;
const iso = (x: number, z: number, h = 0) => ({ x: (x - z) * CS * S, y: (x + z) * 0.5 * S - h * S });

// ------------------------------------------------------------------------------------------------ muebles

function poly(g: PIXI.Graphics, pts: { x: number; y: number }[], fill: number, alpha = 1, stroke = true) {
  g.poly(pts.flatMap((p) => [p.x, p.y])).fill({ color: fill, alpha });
  if (stroke) g.stroke({ width: 1.3, color: INK, alpha: 0.7, join: "round" });
}
/** Caja isométrica: tapa, cara +z (abajo a la izquierda) y cara +x (abajo a la derecha). */
function box(g: PIXI.Graphics, cx: number, cz: number, w: number, d: number, h: number, y0: number, top: number, left: number, right: number) {
  const x0 = cx - w / 2, x1 = cx + w / 2, z0 = cz - d / 2, z1 = cz + d / 2, y1 = y0 + h;
  poly(g, [iso(x0, z1, y0), iso(x1, z1, y0), iso(x1, z1, y1), iso(x0, z1, y1)], left);
  poly(g, [iso(x1, z0, y0), iso(x1, z1, y0), iso(x1, z1, y1), iso(x1, z0, y1)], right);
  poly(g, [iso(x0, z0, y1), iso(x1, z0, y1), iso(x1, z1, y1), iso(x0, z1, y1)], top);
}
function isoEllipse(g: PIXI.Graphics, cx: number, cz: number, r: number, h: number, fill: number, stroke = true) {
  const c = iso(cx, cz, h);
  g.ellipse(c.x, c.y, r * S * Math.SQRT2 * CS, r * S * Math.SQRT2 * 0.5).fill(fill);
  if (stroke) g.stroke({ width: 1.3, color: INK, alpha: 0.7 });
}

function desk(g: PIXI.Graphics, cx: number, cz: number) {
  const W = 1.5, D = 0.75, H = 0.74;
  for (const [sx, sz] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) box(g, cx + sx * (W / 2 - 0.05), cz + sz * (D / 2 - 0.05), 0.05, 0.05, H - 0.04, 0, 0x6b7280, 0x59616d, 0x4b5563);
  box(g, cx, cz - D / 2 + 0.03, W - 0.1, 0.02, 0.35, 0.3, 0xd6c7b0, 0xc4b398, 0xb3a184);
  box(g, cx, cz, W, D, 0.04, H - 0.04, 0xeadfcd, 0xcdbb9f, 0xbca98b);
}
function chair(g: PIXI.Graphics, cx: number, cz: number, backAt: 1 | -1) {
  box(g, cx, cz, 0.05, 0.05, 0.4, 0, 0x6b7280, 0x59616d, 0x4b5563);
  box(g, cx, cz, 0.46, 0.46, 0.06, 0.42, 0x4b5563, 0x3a414c, 0x2f353e);
  box(g, cx, cz + backAt * 0.22, 0.44, 0.05, 0.42, 0.48, 0x4b5563, 0x3a414c, 0x2f353e);
}

// ------------------------------------------------------------------------------------------------ personajes

interface Look { body: number; bodyDark: number; pants: number; skin: number; hair: number; long?: boolean }
interface Pose2 {
  hipY: number; lean: number; legA: [number, number]; knee: [number, number]; armA: [number, number]; elbow: [number, number];
  head: number; sx: number; sy: number; lift: number; folder: boolean; seated: boolean;
}
const basePose = (): Pose2 => ({ hipY: 30, lean: 0, legA: [0, 0], knee: [0, 0], armA: [0.08, -0.05], elbow: [-0.15, -0.15], head: 0, sx: 1, sy: 1, lift: 0, folder: false, seated: false });

/** Una figura de perfil 3/4: `dir` 1 mira a la derecha de la pantalla, -1 a la izquierda; `back` de espaldas. */
function drawPerson(g: PIXI.Graphics, p: Pose2, look: Look, dir: number, back: boolean) {
  g.clear();
  // sombra
  g.ellipse(0, 0, 15, 5).fill({ color: 0x0f172a, alpha: 0.12 });
  const hip = { x: 0, y: -p.hipY - p.lift };
  const sh = { x: hip.x + Math.sin(p.lean) * 24 * dir, y: hip.y - Math.cos(p.lean) * 24 };
  const limb = (o: { x: number; y: number }, a: number, b: number, l1: number, l2: number) => {
    const m = { x: o.x + Math.sin(a) * l1 * dir, y: o.y + Math.cos(a) * l1 };
    const e = { x: m.x + Math.sin(a + b) * l2 * dir, y: m.y + Math.cos(a + b) * l2 };
    return [o, m, e] as const;
  };
  const stroke = (pts: readonly { x: number; y: number }[], w: number, color: number) => {
    g.moveTo(pts[0].x, pts[0].y); for (let i = 1; i < pts.length; i++) g.lineTo(pts[i].x, pts[i].y);
    g.stroke({ width: w, color, cap: "round", join: "round" });
  };
  const legs = [limb(hip, p.legA[0], p.knee[0], 15, 15), limb(hip, p.legA[1], p.knee[1], 15, 15)];
  const arms = [limb({ x: sh.x, y: sh.y + 3 }, p.armA[0], p.elbow[0], 12, 12), limb({ x: sh.x, y: sh.y + 3 }, p.armA[1], p.elbow[1], 12, 12)];
  // lado lejano (más oscuro, detrás del cuerpo)
  stroke(legs[1], 8, 0x2b3038);
  g.circle(legs[1][2].x + 2 * dir, legs[1][2].y, 3.6).fill(0x2b3038);
  stroke(arms[1], 6.5, look.bodyDark);
  // pierna cercana
  stroke(legs[0], 8, look.pants);
  g.roundRect(legs[0][2].x - 3 + 2 * dir, legs[0][2].y - 3, 8, 5, 2.5).fill(0x3b2f2a);
  // torso (un trapecio redondeado que se inclina)
  const tw = 9, hw = 7.5;
  const nx = Math.cos(p.lean), ny = Math.sin(p.lean) * dir;
  g.poly([hip.x - hw * nx, hip.y + 2 - hw * ny, hip.x + hw * nx, hip.y + 2 + hw * ny, sh.x + tw * nx, sh.y + tw * ny, sh.x - tw * nx, sh.y - tw * ny]).fill(look.body);
  g.stroke({ width: 1.2, color: INK, alpha: 0.55, join: "round" });
  if (!back) g.poly([sh.x + 1 * dir, sh.y + 1, sh.x + 5 * dir, sh.y + 1, sh.x + 2 * dir, sh.y + 9]).fill(0xffffff);
  // cabeza
  const hc = { x: sh.x + Math.sin(p.lean + p.head) * 10 * dir, y: sh.y - 9 };
  g.circle(hc.x, hc.y, 8.5).fill(look.skin);
  g.stroke({ width: 1.2, color: INK, alpha: 0.55 });
  if (back) {
    g.circle(hc.x - 0.5 * dir, hc.y - 1, 8.2).fill(look.hair);
  } else {
    g.arc(hc.x - 1.5 * dir, hc.y - 1.5, 8.6, Math.PI * 0.95, Math.PI * 2.05).fill(look.hair);
    g.circle(hc.x + 4.2 * dir, hc.y + 0.5, 1.15).fill(INK);
  }
  if (look.long) g.roundRect(hc.x - (dir > 0 ? 9 : -2), hc.y - 2, 7, 14, 3.5).fill(look.hair);
  // brazo cercano (delante) y la carpeta
  stroke(arms[0], 6.5, look.body);
  g.circle(arms[0][2].x, arms[0][2].y, 3.3).fill(look.skin);
  if (p.folder) {
    const hnd = arms[0][2];
    g.poly([hnd.x - 2 * dir, hnd.y - 10, hnd.x + 12 * dir, hnd.y - 12, hnd.x + 13 * dir, hnd.y + 4, hnd.x - 1 * dir, hnd.y + 6]).fill(ACCENT);
    g.stroke({ width: 1.1, color: INK, alpha: 0.6, join: "round" });
  }
}

/** Pose del agente según el guion (ángulos en radianes: 0 hacia abajo, positivo hacia donde mira). */
function agentPose(f: Frame, t: number): Pose2 {
  const p = basePose();
  p.folder = f.folder === "agent";
  if (f.walking) {
    const ph = f.stride * 3.4, s = Math.sin(ph), amp = 0.35 + 0.65 * f.speed;
    p.legA = [s * 0.55 * amp, -s * 0.55 * amp];
    p.knee = [-Math.max(0, Math.sin(ph + 1.3)) * 0.9 * amp, -Math.max(0, -Math.sin(ph + 1.3)) * 0.9 * amp];
    p.armA = [-s * 0.5 * amp, s * 0.5 * amp];
    p.elbow = [0.35, 0.35];
    if (p.folder) { p.armA[0] = 0.9; p.elbow[0] = 0.9; }
    const c = Math.abs(Math.cos(ph)); // 1 = contacto del pie
    p.lift = (1 - c) * 2.5 * amp;
    p.sy = 1 - c * 0.05 * amp + (1 - c) * 0.03 * amp; p.sx = 1 + c * 0.05 * amp - (1 - c) * 0.02 * amp;
    p.lean = 0.08 * amp;
    return p;
  }
  if (f.seated > 0) {
    const k = smooth(f.seated);
    p.hipY = 30 - 12 * k; p.legA = [1.45 * k, 1.35 * k]; p.knee = [-1.45 * k, -1.35 * k];
    p.seated = k > 0.5;
    // al sentarse: comprime y rebota un poco
    if (f.phase === "sit" || f.phase === "sitBack") { const b = Math.sin(clamp01((f.local - 0.9) / 0.35) * Math.PI); p.sy = 1 - 0.08 * b; p.sx = 1 + 0.06 * b; }
    if (f.typing > 0) {
      const ty = f.typing;
      p.armA = [1.25 * ty, 1.2 * ty]; p.elbow = [0.25 * ty + Math.sin(t * 22) * 0.12 * ty, 0.25 * ty + Math.sin(t * 22 + 2) * 0.12 * ty];
      p.head = 0.1 + Math.sin(t * 2) * 0.03; p.lean = 0.12 * ty;
    }
    return p;
  }
  switch (f.phase) {
    case "talk": {
      if (f.bubble?.who === "agent") { p.armA = [0.9 + Math.sin(t * 5) * 0.35, 0.4 + Math.sin(t * 4 + 1) * 0.2]; p.elbow = [0.8 + Math.sin(t * 7) * 0.3, 0.6]; p.head = Math.sin(t * 3) * 0.08; }
      else { p.head = 0.12 * Math.max(0, Math.sin(t * 6)); }
      p.sy = 1 + Math.sin(t * 2.2) * 0.01;
      break;
    }
    case "give": p.armA = [0.2 + 1.25 * f.reach, 0.1]; p.elbow = [0.25 - 0.25 * f.reach, -0.1]; p.lean = 0.12 * f.reach; break;
    case "approve": p.head = -0.05; break;
    case "celebrate": {
      // anticipación (agacharse) -> salto estirado con los brazos arriba -> aterrizaje que aplasta -> segundo saltito
      const u = f.local;
      const jump = (a: number, b: number, hgt: number) => { const x = clamp01((u - a) / (b - a)); return x > 0 && x < 1 ? Math.sin(x * Math.PI) * hgt : 0; };
      const crouch = Math.sin(clamp01(u / 0.22) * Math.PI) * 1 + Math.sin(clamp01((u - 0.62) / 0.16) * Math.PI) * 0.8 + Math.sin(clamp01((u - 1.08) / 0.14) * Math.PI) * 0.5;
      const air = jump(0.22, 0.62, 22) + jump(0.78, 1.08, 10);
      const inAir = air > 0.5;
      p.lift = air;
      p.sy = 1 - crouch * 0.14 + (inAir ? 0.09 : 0); p.sx = 1 + crouch * 0.12 - (inAir ? 0.06 : 0);
      p.knee = [-crouch * 0.9, -crouch * 0.9]; p.legA = [crouch * 0.45, crouch * 0.45]; p.hipY = 30 - crouch * 4;
      const up = smooth(clamp01((u - 0.15) / 0.15)) * (1 - smooth(clamp01((u - 1.45) / 0.3)));
      p.armA = [Math.PI * 0.92 * up + 0.08, Math.PI * 0.85 * up - 0.05]; p.elbow = [-0.2 * up, -0.2 * up];
      p.head = -0.15 * up;
      break;
    }
  }
  return p;
}

function partnerPose(f: Frame, t: number): Pose2 {
  const p = basePose();
  const m = f.partner.mode;
  if (m === "talk") { p.armA = [0.7 + Math.sin(t * 5.5) * 0.35, 0.3]; p.elbow = [0.9 + Math.sin(t * 6) * 0.3, 0.4]; p.head = Math.sin(t * 3) * 0.08; }
  else if (m === "listen") p.head = 0.14 * Math.max(0, Math.sin(t * 5));
  else if (m === "hand") { p.armA = [1.35, 0.1]; p.elbow = [0, 0]; p.folder = true; }
  p.sy = 1 + Math.sin(t * 2) * 0.008;
  return p;
}
function bossPose(f: Frame, t: number): Pose2 {
  const p = basePose();
  p.hipY = 18; p.legA = [1.45, 1.35]; p.knee = [-1.45, -1.35]; p.seated = true;
  const m = f.boss.mode;
  if (m === "work") { p.armA = [1.2, 1.15]; p.elbow = [0.3 + Math.sin(t * 20) * 0.1, 0.3 + Math.sin(t * 20 + 2) * 0.1]; p.head = 0.1; }
  else if (m === "receive") { p.armA = [1.3 * Math.sin(Math.PI * clamp01(f.boss.u)), 0.1]; p.elbow = [0, 0]; }
  else if (m === "approve") { p.head = 0.2 * Math.max(0, Math.sin(f.boss.u * Math.PI * 4)); p.armA = [1.0, 0.2]; p.elbow = [0.8, 0]; }
  else if (m === "clap") { const c = Math.abs(Math.sin(t * 12)); p.armA = [1.3, 1.2]; p.elbow = [0.6 + c * 0.4, 0.6 + c * 0.4]; }
  return p;
}

function facing(yaw: number): { dir: number; back: boolean } {
  const dx = Math.sin(yaw), dz = Math.cos(yaw);
  const sx = dx - dz, sy = (dx + dz) * 0.5;
  return { dir: sx >= 0 ? 1 : -1, back: sy < -0.05 };
}

// ------------------------------------------------------------------------------------------------ escena

export default function PanelC({ active }: { active: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const appRef = useRef<PIXI.Application | null>(null);
  const activeRef = useRef(active);
  activeRef.current = active;

  useEffect(() => {
    const el = host.current!;
    let disposed = false;
    let app: PIXI.Application | null = null;
    (async () => {
      const P = await import("pixi.js");
      const a = new P.Application();
      await a.init({ resizeTo: el, background: 0xffffff, antialias: true, resolution: Math.min(2, window.devicePixelRatio || 1), autoDensity: true });
      if (disposed) { a.destroy(true, { children: true }); return; }
      app = a; appRef.current = a;
      a.ticker.maxFPS = 30;
      el.appendChild(a.canvas);
      a.canvas.style.display = "block";

      const world = new P.Container();
      world.sortableChildren = true;
      a.stage.addChild(world);
      const layer = (z: number) => { const g = new P.Graphics(); g.zIndex = z; world.addChild(g); return g; };
      const { x0, x1, z0, z1 } = PLAN.room;

      // suelo con retícula suave y paredes al fondo
      const floor = layer(-1000);
      poly(floor, [iso(x0, z0), iso(x1, z0), iso(x1, z1), iso(x0, z1)], 0xf3f5f8);
      for (let x = Math.ceil(x0); x <= x1; x++) floor.moveTo(iso(x, z0).x, iso(x, z0).y).lineTo(iso(x, z1).x, iso(x, z1).y);
      for (let z = Math.ceil(z0); z <= z1; z++) floor.moveTo(iso(x0, z).x, iso(x0, z).y).lineTo(iso(x1, z).x, iso(x1, z).y);
      floor.stroke({ width: 1, color: 0xdfe4ec });
      const H = 2.4;
      poly(floor, [iso(x0, z0), iso(x1, z0), iso(x1, z0, H), iso(x0, z0, H)], 0xfafbfc);
      poly(floor, [iso(x0, z0), iso(x0, z1), iso(x0, z1, H), iso(x0, z0, H)], 0xedf0f4);
      poly(floor, [iso(-2.2, z0, 1.05), iso(0.2, z0, 1.05), iso(0.2, z0, 2.05), iso(-2.2, z0, 2.05)], 0xdbeafe);
      poly(floor, [iso(1.4, z0, 1.05), iso(3.6, z0, 1.05), iso(3.6, z0, 2.05), iso(1.4, z0, 2.05)], 0xdbeafe);
      floor.moveTo(iso(x0, z0, 0.9).x, iso(x0, z0, 0.9).y).lineTo(iso(x1, z0, 0.9).x, iso(x1, z0, 0.9).y).stroke({ width: 3, color: ACCENT, alpha: 0.8 });
      // puerta
      poly(floor, [iso(x0, PLAN.door[1] - 0.5), iso(x0, PLAN.door[1] + 0.5), iso(x0, PLAN.door[1] + 0.5, 2.05), iso(x0, PLAN.door[1] - 0.5, 2.05)], 0xc9d2de);
      // alfombra de la reunión y de la zona de aprobación
      const rug = layer(-900);
      isoEllipse(rug, PLAN.meeting.pos[0], PLAN.meeting.pos[1], 1.45, 0, 0xe3e9f2);
      poly(rug, [iso(1.9, -3.2), iso(4.6, -3.2), iso(4.6, -0.3), iso(1.9, -0.3)], 0xf1ece4);

      // muebles (cada uno en su capa, ordenados por profundidad x+z)
      const d1 = layer(PLAN.desk.pos[0] + PLAN.desk.pos[1]); desk(d1, PLAN.desk.pos[0], PLAN.desk.pos[1]);
      // monitor del agente (pantalla hacia +z, la vemos)
      const mx = PLAN.desk.pos[0], mz = PLAN.desk.pos[1] - 0.12;
      box(d1, mx, mz - 0.05, 0.05, 0.05, 0.12, 0.74, 0x9ca3af, 0x6b7280, 0x4b5563);
      box(d1, mx, mz, 0.62, 0.04, 0.4, 0.84, 0x374151, 0x1f2937, 0x111827);
      const screen = layer(PLAN.desk.pos[0] + PLAN.desk.pos[1] + 0.01);
      const glow = layer(PLAN.desk.pos[0] + PLAN.desk.pos[1] + 0.02);
      glow.blendMode = "add";
      box(d1, mx, PLAN.desk.pos[1] + 0.2, 0.42, 0.14, 0.02, 0.74, 0xe5e7eb, 0xd1d5db, 0xc4c9d1);
      const c1 = layer(PLAN.seat[0] + PLAN.seat[1] + 0.05); chair(c1, PLAN.seat[0], PLAN.seat[1] + 0.06, 1);
      const d2 = layer(PLAN.bossDesk.pos[0] + PLAN.bossDesk.pos[1] + 0.8); desk(d2, PLAN.bossDesk.pos[0], PLAN.bossDesk.pos[1]);
      // portátil de la jefa (lo vemos por detrás) y lámpara
      box(d2, PLAN.bossDesk.pos[0] + 0.1, PLAN.bossDesk.pos[1] - 0.05, 0.5, 0.34, 0.02, 0.74, 0x9ca3af, 0x6b7280, 0x4b5563);
      box(d2, PLAN.bossDesk.pos[0] + 0.1, PLAN.bossDesk.pos[1] + 0.12, 0.5, 0.02, 0.32, 0.76, 0xb6bcc6, 0x9ca3af, 0x8b929d);
      box(d2, PLAN.bossDesk.pos[0] + 0.6, PLAN.bossDesk.pos[1] - 0.2, 0.12, 0.12, 0.35, 0.74, 0xfff1d6, 0xf3dfb8, 0xe5cf9f);
      const c2 = layer(PLAN.boss[0] + PLAN.boss[1] - 0.05); chair(c2, PLAN.boss[0], PLAN.boss[1] - 0.06, -1);
      const tbl = layer(PLAN.meeting.pos[0] + PLAN.meeting.pos[1]);
      box(tbl, PLAN.meeting.pos[0], PLAN.meeting.pos[1], 0.08, 0.08, 0.72, 0, 0x6b7280, 0x59616d, 0x4b5563);
      isoEllipse(tbl, PLAN.meeting.pos[0], PLAN.meeting.pos[1], PLAN.meeting.r, 0.72, 0xcdbb9f);
      isoEllipse(tbl, PLAN.meeting.pos[0], PLAN.meeting.pos[1], PLAN.meeting.r, 0.75, 0xeadfcd);
      // ambiente: estantería, plantas, sofá
      const deco = layer(-50);
      box(deco, -0.2, -3.35, 1.6, 0.35, 1.7, 0, 0xeadfcd, 0xd6c7b0, 0xc4b398);
      for (const y of [0.45, 0.9, 1.35]) poly(deco, [iso(-1.0, -3.17, y), iso(0.6, -3.17, y), iso(0.6, -3.17, y + 0.03), iso(-1.0, -3.17, y + 0.03)], 0xb3a184, 1, false);
      for (let i = 0; i < 9; i++) { const x = -0.9 + i * 0.17, y = 0.48 + (i % 3) * 0.45; poly(deco, [iso(x, -3.18, y), iso(x + 0.1, -3.18, y), iso(x + 0.1, -3.18, y + 0.3), iso(x, -3.18, y + 0.3)], [0x94a3b8, 0x0a66c2, 0xcbd5e1, 0x64748b][i % 4], 1, false); }
      const plant = (x: number, z: number) => {
        const g = layer(x + z); box(g, x, z, 0.36, 0.36, 0.42, 0, 0xe5e7eb, 0xd1d5db, 0xc4c9d1);
        const c = iso(x, z, 0.75);
        for (const [dx, dy, r] of [[-7, 4, 12], [8, 2, 11], [0, -8, 13], [-3, 10, 9]]) g.circle(c.x + dx, c.y + dy, r).fill(0x6f9a7d);
        g.circle(c.x, c.y - 8, 13).stroke({ width: 1.2, color: INK, alpha: 0.5 });
      };
      plant(-4.7, -3.1); plant(4.7, 2.9); plant(2.0, -3.25);
      const sofa = layer(-3.2 + 2.85);
      box(sofa, -3.2, 2.85, 1.9, 0.8, 0.42, 0, 0x64748b, 0x4b5563, 0x3f4856);
      box(sofa, -3.2, 3.15, 1.9, 0.2, 0.4, 0.42, 0x64748b, 0x4b5563, 0x3f4856);

      // carpeta suelta y sello de aprobado
      const folderG = layer(0);
      const stamp = layer(PLAN.bossDesk.pos[0] + PLAN.bossDesk.pos[1] + 0.9);
      // personajes
      const people = {
        agent: { g: new P.Graphics(), look: { body: 0x2f5f9e, bodyDark: 0x244a7c, pants: 0x374151, skin: 0xf1c7a5, hair: 0x4a3626 } as Look },
        partner: { g: new P.Graphics(), look: { body: 0x7b8a9b, bodyDark: 0x5f6c7b, pants: 0x4b5563, skin: 0xd9a27e, hair: 0x22252b, long: true } as Look },
        boss: { g: new P.Graphics(), look: { body: 0x7a4b5c, bodyDark: 0x5e3846, pants: 0x374151, skin: 0xf3d1b5, hair: 0x8a5a3a, long: true } as Look },
      };
      for (const p of Object.values(people)) world.addChild(p.g);
      const sparks = layer(100);
      // bocadillo
      const bubble = new P.Container(); bubble.zIndex = 1000; world.addChild(bubble);
      const bg = new P.Graphics(); bubble.addChild(bg);
      const txt = new P.Text({ text: "", style: { fontFamily: "IBM Plex Sans, Inter, system-ui, sans-serif", fontSize: 12, fontWeight: "500", fill: 0x1f2937 }, resolution: 3 });
      bubble.addChild(txt);
      let bubbleText = "";

      const fit = () => {
        const w = a.screen.width, h = a.screen.height;
        const left = iso(x0, z1).x, right = iso(x1, z0).x, top = iso(x0, z0, H).y, bottom = iso(x1, z1).y;
        const k = Math.min(w / (right - left + 30), h / (bottom - top + 30));
        world.scale.set(k);
        world.position.set(w / 2 - ((left + right) / 2) * k, h / 2 - ((top + bottom) / 2) * k + 6);
      };

      a.ticker.add(() => {
        fit();
        const t = labTime(), f = sample(t);
        // pantalla del agente: se ilumina y "escribe" líneas
        screen.clear(); glow.clear();
        const sx0 = mx - 0.28, sx1 = mx + 0.28, sz = mz + 0.025;
        const on = f.screen;
        poly(screen, [iso(sx0, sz, 0.87), iso(sx1, sz, 0.87), iso(sx1, sz, 1.2), iso(sx0, sz, 1.2)], on > 0.05 ? 0xeaf3fc : 0x1f2937, 1, false);
        if (on > 0.05) {
          const n = f.typing > 0 ? Math.floor((t * 4) % 7) + 1 : 5;
          for (let i = 0; i < n; i++) { const y = 1.15 - i * 0.04, w = 0.1 + ((i * 37) % 30) / 100; poly(screen, [iso(sx0 + 0.04, sz, y), iso(sx0 + 0.04 + w, sz, y), iso(sx0 + 0.04 + w, sz, y - 0.018), iso(sx0 + 0.04, sz, y - 0.018)], i === 0 ? ACCENT : 0x64748b, on, false); }
          const c = iso(mx, sz, 1.03);
          glow.ellipse(c.x, c.y + 4, 34, 22).fill({ color: 0x9cc8f5, alpha: 0.18 * on + 0.12 * f.typing * (0.6 + 0.4 * Math.sin(t * 9)) });
        }
        // carpeta suelta
        folderG.clear();
        if (f.folder === "table" || f.folder === "boss") {
          const [cx, cz, y] = f.folder === "table" ? [PLAN.meeting.pos[0] + 0.2, PLAN.meeting.pos[1] - 0.05, 0.76] : [PLAN.bossDesk.pos[0] - 0.35, PLAN.bossDesk.pos[1] + 0.12, 0.765];
          folderG.zIndex = f.folder === "table" ? PLAN.meeting.pos[0] + PLAN.meeting.pos[1] + 0.01 : PLAN.bossDesk.pos[0] + PLAN.bossDesk.pos[1] + 0.85;
          box(folderG, cx, cz, 0.26, 0.34, 0.02, y, ACCENT, 0x084f96, 0x074582);
        }
        // sello verde que aparece con rebote
        stamp.clear();
        if (f.approved && f.t < 23.5) {
          const u = clamp01((f.t - 19.1) / 0.35);
          const k = u < 1 ? 1.25 * Math.sin(u * Math.PI * 0.5) + (1 - u) * 0 : 1;
          const c = iso(PLAN.bossDesk.pos[0] - 0.35, PLAN.bossDesk.pos[1] + 0.12, 0.78);
          stamp.circle(c.x, c.y - 14, 10 * k).fill(0x16a34a).stroke({ width: 2, color: 0xffffff });
          stamp.moveTo(c.x - 4.5 * k, c.y - 14).lineTo(c.x - 1 * k, c.y - 10.5 * k + (1 - k) * 0).lineTo(c.x + 5 * k, c.y - 18.5 * k).stroke({ width: 2.4, color: 0xffffff, cap: "round", join: "round" });
        }
        // personas
        const place = (o: PIXI.Graphics, pos: V2, z: number, pose: Pose2, look: Look, yaw: number) => {
          const fc = facing(yaw);
          drawPerson(o, pose, look, fc.dir, fc.back);
          const c = iso(pos[0], pos[1]);
          o.position.set(c.x, c.y);
          o.scale.set(pose.sx, pose.sy);
          o.zIndex = z;
        };
        // sentado en la silla: un poco hacia el respaldo
        const ap = agentPose(f, t);
        place(people.agent.g, f.pos, f.pos[0] + f.pos[1] + 0.02, ap, people.agent.look, f.yaw);
        place(people.partner.g, PLAN.partner, PLAN.partner[0] + PLAN.partner[1], partnerPose(f, t), people.partner.look, f.partner.yaw);
        place(people.boss.g, PLAN.boss, PLAN.boss[0] + PLAN.boss[1], bossPose(f, t), people.boss.look, 0);
        // celebración: tres trazos cortos que salen de la cabeza (sobrios, en el azul de la marca)
        sparks.clear();
        if (f.phase === "celebrate") {
          const c = iso(f.pos[0], f.pos[1]);
          const u = clamp01((f.local - 0.25) / 0.6), al = 1 - clamp01((f.local - 1.0) / 0.6);
          if (u > 0 && al > 0) for (const ang of [-0.9, 0, 0.9]) {
            const r0 = 50 + u * 14, r1 = r0 + 10 * (1 - u * 0.4);
            sparks.moveTo(c.x + Math.sin(ang) * r0 * 0.55, c.y - 40 - Math.cos(ang) * r0 * 0.5).lineTo(c.x + Math.sin(ang) * r1 * 0.55, c.y - 40 - Math.cos(ang) * r1 * 0.5)
              .stroke({ width: 2.5, color: ACCENT, alpha: al, cap: "round" });
          }
          sparks.zIndex = f.pos[0] + f.pos[1] + 0.03;
        }
        // bocadillo
        const b = f.bubble;
        bubble.visible = !!b;
        if (b) {
          if (b.text !== bubbleText) {
            bubbleText = b.text; txt.text = b.text;
            const w = txt.width + 20, h = txt.height + 12;
            bg.clear();
            bg.roundRect(-w / 2, -h, w, h, 10).fill(0xffffff).stroke({ width: 1.2, color: INK, alpha: 0.6 });
            bg.poly([-6, -1, 6, -1, -2, 8]).fill(0xffffff);
            bg.moveTo(-6, 0).lineTo(-2, 8).lineTo(6, 0).stroke({ width: 1.2, color: INK, alpha: 0.6 });
            txt.position.set(-txt.width / 2, -h + 6);
          }
          const pos = b.who === "agent" ? f.pos : b.who === "partner" ? PLAN.partner : PLAN.boss;
          const c = iso(pos[0], pos[1]);
          const pop = smooth((f.t - b.t0) / 0.18);
          bubble.position.set(c.x, c.y - (b.who === "boss" ? 70 : 84));
          bubble.scale.set(0.8 + 0.2 * pop);
          bubble.alpha = pop;
        }
      });
      if (!activeRef.current) a.ticker.stop();
    })();
    return () => {
      disposed = true;
      appRef.current = null;
      app?.destroy(true, { children: true });
    };
  }, []);

  useEffect(() => {
    const a = appRef.current;
    if (!a) return;
    if (active) a.ticker.start(); else a.ticker.stop();
  }, [active]);

  return <div ref={host} className="absolute inset-0" />;
}
