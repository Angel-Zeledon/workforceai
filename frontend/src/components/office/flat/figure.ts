// The people of the "Plano" office: a 3/4 side-on figure drawn every frame from a 2D pose (isoMath.mapPose), in the same
// illustrated manner as the lab's panel C (ink outline, flat fills). Pixi is only a type here: the scene passes the Graphics.
import type * as PIXI from "pixi.js";
import { shade, type Pose2 } from "./isoMath";

export const INK = 0x1f2a37;
export const ACCENT = 0x0a66c2;

export interface Look {
  body: number; bodyDark: number; pants: number; skin: number; hair: number; hairStyle: string; accessory: string;
}
/** What is drawn on top of the figure besides the body. */
export interface Extras { mug: boolean; badge: "" | "check" | "alert" | "warn" | "think"; docs: boolean; selectedColor: number | null; t: number }

export const lookOf = (color: number, skin: number, hair: number, hairStyle: string, accessory: string): Look =>
  ({ body: color, bodyDark: shade(color, 0.78), pants: 0x374151, skin, hair, hairStyle, accessory });

type Pt = { x: number; y: number };

/** Draws one figure around its feet at (0, 0); `dir` 1 looks to the right of the screen, -1 to the left; `back` = from behind. */
export function drawPerson(g: PIXI.Graphics, p: Pose2, look: Look, dir: number, back: boolean, ex: Extras) {
  g.clear();
  g.ellipse(0, 0, 15, 5).fill({ color: 0x0f172a, alpha: 0.13 });
  if (ex.selectedColor !== null) g.ellipse(0, 0, 23, 8.5).stroke({ width: 2.4, color: ex.selectedColor, alpha: 0.95 });
  const hip: Pt = { x: 0, y: -p.hipY };
  const sh: Pt = { x: hip.x + Math.sin(p.lean) * 24 * dir, y: hip.y - Math.cos(p.lean) * 24 };
  const limb = (o: Pt, a: number, b: number, l1: number, l2: number) => {
    const m = { x: o.x + Math.sin(a) * l1 * dir, y: o.y + Math.cos(a) * l1 };
    const e = { x: m.x + Math.sin(a + b) * l2 * dir, y: m.y + Math.cos(a + b) * l2 };
    return [o, m, e] as const;
  };
  const stroke = (pts: readonly Pt[], w: number, color: number) => {
    g.moveTo(pts[0].x, pts[0].y);
    for (let i = 1; i < pts.length; i++) g.lineTo(pts[i].x, pts[i].y);
    g.stroke({ width: w, color, cap: "round", join: "round" });
  };
  const legs = [limb(hip, p.legA[0], p.knee[0], 15, 15), limb(hip, p.legA[1], p.knee[1], 15, 15)];
  const shoulder = { x: sh.x, y: sh.y + 3 };
  const arms = [limb(shoulder, p.armA[0], p.elbow[0], 12, 12), limb(shoulder, p.armA[1], p.elbow[1], 12, 12)];
  // far side (darker, behind the body)
  stroke(legs[1], 8, 0x2b3038);
  g.circle(legs[1][2].x + 2 * dir, legs[1][2].y, 3.6).fill(0x2b3038);
  stroke(arms[1], 6.5, look.bodyDark);
  g.circle(arms[1][2].x, arms[1][2].y, 3).fill(shade(look.skin, 0.9));
  // near leg + shoe
  stroke(legs[0], 8, look.pants);
  g.roundRect(legs[0][2].x - 3 + 2 * dir, legs[0][2].y - 3, 8, 5, 2.5).fill(0x3b2f2a);
  // torso: a rounded trapezium that leans
  const tw = 9, hw = 7.5;
  const nx = Math.cos(p.lean), ny = Math.sin(p.lean) * dir;
  g.poly([hip.x - hw * nx, hip.y + 2 - hw * ny, hip.x + hw * nx, hip.y + 2 + hw * ny, sh.x + tw * nx, sh.y + tw * ny, sh.x - tw * nx, sh.y - tw * ny]).fill(look.body);
  g.stroke({ width: 1.2, color: INK, alpha: 0.55, join: "round" });
  if (!back) g.poly([sh.x + 1 * dir, sh.y + 1, sh.x + 5 * dir, sh.y + 1, sh.x + 2 * dir, sh.y + 9]).fill(0xffffff);
  // head
  const hc = { x: sh.x + Math.sin(p.lean + p.head) * 10 * dir, y: sh.y - 9 };
  const hs = look.hairStyle;
  if (!back && (hs === "long" || hs === "bob")) g.roundRect(hc.x - (dir > 0 ? 9.5 : -2.5), hc.y - 3, 7, hs === "long" ? 15 : 10, 3.5).fill(look.hair);
  g.circle(hc.x, hc.y, 8.5).fill(look.skin);
  g.stroke({ width: 1.2, color: INK, alpha: 0.55 });
  if (back) {
    g.circle(hc.x - 0.5 * dir, hc.y - 1, 8.4).fill(look.hair);
    if (hs === "long") g.roundRect(hc.x - 7.5, hc.y, 15, 13, 5).fill(look.hair);
    else if (hs === "bob") g.roundRect(hc.x - 8, hc.y, 16, 9, 4).fill(look.hair);
    else if (hs === "ponytail") g.ellipse(hc.x - 9 * dir, hc.y + 5, 3.5, 7).fill(look.hair);
    if (hs === "bald") g.circle(hc.x, hc.y, 8.2).fill(look.skin);
  } else if (hs !== "bald") {
    g.arc(hc.x - 1.5 * dir, hc.y - 1.5, 8.7, Math.PI * 0.95, Math.PI * 2.05).fill(look.hair);
    if (hs === "ponytail") g.ellipse(hc.x - 9.5 * dir, hc.y + 1, 3, 6).fill(look.hair);
  }
  if (hs === "bun") g.circle(hc.x - 1 * dir, hc.y - 10, 4.2).fill(look.hair);
  if (!back) {
    g.circle(hc.x + 4.2 * dir, hc.y + 0.5, 1.15).fill(INK);
    if (look.accessory === "glasses" || look.accessory === "roundglasses") g.circle(hc.x + 4.4 * dir, hc.y + 0.5, 2.8).stroke({ width: 1, color: INK, alpha: 0.8 });
  }
  if (look.accessory === "headphones" || look.accessory === "headset") {
    g.arc(hc.x, hc.y - 1, 9.4, Math.PI * 1.05, Math.PI * 1.95).stroke({ width: 2, color: 0x374151 });
    g.circle(hc.x - 1 * dir, hc.y + 1, 3).fill(0x374151);
  } else if (look.accessory === "hardhat") {
    g.arc(hc.x, hc.y - 2, 9.4, Math.PI, Math.PI * 2).fill(0xf5c542);
    g.stroke({ width: 1, color: INK, alpha: 0.5 });
  }
  // near arm (in front) and what it holds
  stroke(arms[0], 6.5, look.body);
  g.circle(arms[0][2].x, arms[0][2].y, 3.3).fill(look.skin);
  if (ex.mug) {
    const h = arms[0][2];
    g.roundRect(h.x + 1 * dir - 3, h.y - 7, 6, 7, 1.5).fill(0xf8fafc);
    g.stroke({ width: 1, color: INK, alpha: 0.6 });
  }
  if (ex.docs) {
    const h = arms[0][2];
    g.poly([h.x - 2 * dir, h.y - 10, h.x + 12 * dir, h.y - 12, h.x + 13 * dir, h.y + 4, h.x - 1 * dir, h.y + 6]).fill(ACCENT);
    g.stroke({ width: 1.1, color: INK, alpha: 0.6, join: "round" });
  }
  // status badge over the head
  if (ex.badge) {
    const bx = hc.x, by = hc.y - 21 + Math.sin(ex.t * 3) * 1.2;
    if (ex.badge === "think") {
      for (let i = 0; i < 3; i++) g.circle(bx - 7 + i * 7, by + Math.sin(ex.t * 4 + i * 0.9) * 1.6, 2 + 0.5 * Math.sin(ex.t * 4 + i * 0.9)).fill(0x64748b);
    } else {
      const c = ex.badge === "check" ? 0x16a34a : ex.badge === "warn" ? 0xd97706 : 0xdc2626;
      g.circle(bx, by, 7.5).fill(c).stroke({ width: 1.6, color: 0xffffff });
      if (ex.badge === "check") g.moveTo(bx - 3.5, by).lineTo(bx - 1, by + 2.6).lineTo(bx + 3.6, by - 2.8).stroke({ width: 2, color: 0xffffff, cap: "round", join: "round" });
      else { g.moveTo(bx, by - 3.8).lineTo(bx, by + 0.8).stroke({ width: 2, color: 0xffffff, cap: "round" }); g.circle(bx, by + 3.6, 1.1).fill(0xffffff); }
    }
  }
}
