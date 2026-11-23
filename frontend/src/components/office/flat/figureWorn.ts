// Vector drawing of the shop items on the "Plano" figure (hats, glasses, headphones, scarf, badge, shoulder companion).
// Same shapes as the 3D character in simplified form; everything is drawn with ink outline like the rest of the figure.
import type * as PIXI from "pixi.js";
import { shade } from "./isoMath";
import type { WornLook } from "./wornLook";

const INK = 0x1f2a37;
const line = (g: PIXI.Graphics, w = 1.1) => g.stroke({ width: w, color: INK, alpha: 0.6, join: "round" });

/** A hat on a head centered at (hx, hy) (radius 8.5), looking toward `dir`. */
export function drawHat(g: PIXI.Graphics, h: NonNullable<WornLook["hat"]>, hx: number, hy: number, dir: number) {
  const r = Math.min(10.5, Math.max(5, h.r * 0.95));
  const base = hy - 6.2;
  const hh = Math.max(3, Math.min(20, h.h));
  const x0 = hx + h.tilt * -4 * dir;
  const brim = () => { if (h.brim > 0) { g.ellipse(x0, base + 1, Math.min(14, h.brim * 0.95), 2.6).fill(h.c1); line(g); } };
  switch (h.crown) {
    case "cyl": case "trunc": case "shako": case "mushroom": {
      brim();
      g.roundRect(x0 - r * 0.8, base - hh, r * 1.6, hh + 1, 1.5).fill(h.c1); line(g);
      if (h.band) g.rect(x0 - r * 0.8, base - 3, r * 1.6, 2.4).fill(h.c2);
      break;
    }
    case "cone": case "jester": {
      g.poly([x0 - r * 0.9, base + 1, x0 + r * 0.9, base + 1, x0 + (h.crown === "jester" ? 4 * dir : 0), base - hh * 1.1]).fill(h.c1); line(g);
      if (h.band) g.rect(x0 - r * 0.85, base - 1, r * 1.7, 2).fill(h.c2);
      break;
    }
    case "puff": {
      g.roundRect(x0 - r * 0.75, base - 3, r * 1.5, 4.5, 1.5).fill(h.c2);
      for (const dx of [-0.6, 0, 0.6]) g.circle(x0 + dx * r, base - 5 - (dx === 0 ? 3 : 0), r * 0.62).fill(h.c1);
      line(g, 0.9);
      break;
    }
    case "board": case "flat": {
      g.poly([x0 - r * 1.25, base - 1, x0, base - 4.5, x0 + r * 1.25, base - 1, x0, base + 2]).fill(h.c1); line(g);
      g.roundRect(x0 - r * 0.6, base + 0.5, r * 1.2, 3, 1).fill(h.c1);
      g.moveTo(x0 + r * 1.05, base - 1).lineTo(x0 + r * 1.05, base + 5).stroke({ width: 1.3, color: h.c2 });
      break;
    }
    case "crown": case "tiara": {
      const w = r * 0.95, t = Math.max(4, hh * 0.9);
      g.poly([x0 - w, base + 1, x0 - w, base - t * 0.5, x0 - w * 0.5, base - t * 0.1, x0, base - t, x0 + w * 0.5, base - t * 0.1, x0 + w, base - t * 0.5, x0 + w, base + 1]).fill(h.c1); line(g);
      g.circle(x0, base - 1.5, 1.3).fill(h.c2);
      break;
    }
    case "turban": case "donut": case "cloud": case "bicorne": case "lobes": {
      g.ellipse(x0, base - 1.5, r * 1.1, Math.max(4, hh * 0.5)).fill(h.c1); line(g);
      if (h.band) g.ellipse(x0, base + 0.3, r * 1.05, 1.7).fill(h.c2);
      break;
    }
    case "wreath": {
      g.arc(x0, hy - 1, 9.3, Math.PI * 1.05, Math.PI * 1.95).stroke({ width: 3, color: h.c1 });
      g.circle(x0 + 5 * dir, hy - 9, 1.6).fill(h.c2);
      break;
    }
    case "none": {
      if (h.band) g.arc(x0, hy - 1, 9, Math.PI * 1.02, Math.PI * 1.98).stroke({ width: 2.4, color: h.c1 });
      break;
    }
    default: { // dome (cap, beanie, beret...) and the odd shapes
      const front = h.front > 0 ? Math.min(9, h.front * 0.9) : 0;
      g.arc(x0, base + 1, r, Math.PI, Math.PI * 2).fill(h.c1); line(g);
      if (front) { g.roundRect(dir > 0 ? x0 : x0 - front - r * 0.4, base - 0.5, front + r * 0.4, 2.2, 1).fill(h.c1); line(g, 0.9); }
      if (h.band) g.rect(x0 - r, base - 0.5, r * 2, 2).fill(h.c2);
      if (h.deco.includes("pompom")) g.circle(x0, base - Math.min(r, hh) - 1.5, 2.6).fill(h.c2);
    }
  }
  if (h.deco.includes("tassel") && h.crown !== "board") g.circle(x0, base - hh - 1, 1.6).fill(h.c2);
}

export function drawGlasses(g: PIXI.Graphics, w: NonNullable<WornLook["glasses"]>, hx: number, hy: number, dir: number) {
  const ex = hx + 4.4 * dir, ey = hy + 0.5;
  if (w.frame === "visor" || w.frame === "goggles" || w.frame === "welder" || w.frame === "ski") {
    g.roundRect(ex - 4, ey - 2.6, 8.5, 5, 2).fill({ color: w.color, alpha: 0.55 + w.tint * 0.3 }); line(g, 1);
  } else {
    g.circle(ex, ey, 3).fill({ color: w.color, alpha: 0.25 + w.tint * 0.35 });
    g.circle(ex, ey, 3).stroke({ width: 1.1, color: w.frame === "rimless" ? 0x94a3b8 : INK, alpha: 0.85 });
  }
  g.moveTo(ex - 3 * dir, ey).lineTo(hx - 6 * dir, ey - 0.5).stroke({ width: 0.9, color: INK, alpha: 0.7 });
}

export function drawCans(g: PIXI.Graphics, c: NonNullable<WornLook["cans"]>, hx: number, hy: number, dir: number) {
  if (c.kind === "buds") { g.circle(hx - 1 * dir, hy + 2, 1.8).fill(c.color); return; }
  g.arc(hx, hy - 1, 9.6, Math.PI * 1.05, Math.PI * 1.95).stroke({ width: 2.2, color: c.color });
  g.circle(hx - 1 * dir, hy + 1.5, c.kind === "muffs" ? 4 : 3.2).fill(c.color); line(g, 0.9);
}

export function drawNeck(g: PIXI.Graphics, n: NonNullable<WornLook["neck"]>, sx: number, sy: number, dir: number) {
  if (n.bow) {
    g.poly([sx + 2 * dir, sy + 3, sx - 3 * dir, sy + 0, sx - 3 * dir, sy + 6]).fill(n.c1);
    g.poly([sx + 2 * dir, sy + 3, sx + 7 * dir, sy + 0, sx + 7 * dir, sy + 6]).fill(n.c1);
    g.circle(sx + 2 * dir, sy + 3, 1.6).fill(n.c2);
    return;
  }
  g.roundRect(sx - 6, sy - 0.5, 12, 4, 2).fill(n.c1); line(g, 0.9);
  if (n.pattern !== "plain") for (let i = 0; i < 3; i++) g.rect(sx - 4 + i * 4, sy - 0.2, 1.6, 3.4).fill(n.c2);
  g.roundRect(sx - 4 * dir - 2, sy + 2, 4, 10, 1.5).fill(n.c1); line(g, 0.9);
  if (n.pattern !== "plain") g.rect(sx - 4 * dir - 2, sy + 6, 4, 1.6).fill(n.c2);
}

export function drawBadge(g: PIXI.Graphics, b: NonNullable<WornLook["badge"]>, x: number, y: number) {
  const r = 3.2;
  if (b.frame === "hex") g.poly(Array.from({ length: 6 }, (_, i) => [x + Math.cos(i * Math.PI / 3) * r, y + Math.sin(i * Math.PI / 3) * r]).flat()).fill(b.color);
  else if (b.frame === "diamond") g.poly([x, y - r - 0.5, x + r, y, x, y + r + 0.5, x - r, y]).fill(b.color);
  else if (b.frame === "shield") g.poly([x - r, y - r, x + r, y - r, x + r, y + 0.5, x, y + r + 1, x - r, y + 0.5]).fill(b.color);
  else if (b.frame === "star") g.poly(Array.from({ length: 10 }, (_, i) => [x + Math.cos(-Math.PI / 2 + i * Math.PI / 5) * (i % 2 ? r * 0.5 : r * 1.1), y + Math.sin(-Math.PI / 2 + i * Math.PI / 5) * (i % 2 ? r * 0.5 : r * 1.1)]).flat()).fill(b.color);
  else g.circle(x, y, r).fill(b.color);
  line(g, 0.8);
  g.circle(x, y, 1).fill(b.color2);
}

/** The shoulder companion (duck, robot or parrot): a small bobbing figure behind the shoulder. */
export function drawBuddy(g: PIXI.Graphics, b: NonNullable<WornLook["buddy"]>, x: number, y: number, dir: number, t: number) {
  const by = y + Math.sin(t * 3.1) * 0.7;
  if (b.kind === "robot") {
    g.roundRect(x - 4, by - 4, 8, 7, 1.5).fill(b.c1); line(g, 0.9);
    g.moveTo(x, by - 4).lineTo(x, by - 8).stroke({ width: 1, color: 0x59616d });
    g.circle(x, by - 8.6, 1.3).fill(b.c2);
    g.circle(x + 1.5 * dir, by - 1.2, 1.1).fill(b.c2);
  } else {
    g.ellipse(x, by, 4.2, 5).fill(b.c1); line(g, 0.9);
    g.circle(x + 1 * dir, by - 5.5, 3.2).fill(b.c1); line(g, 0.9);
    g.poly([x + 3.6 * dir, by - 5.8, x + 7 * dir, by - 5.2, x + 3.6 * dir, by - 4.2]).fill(b.kind === "parrot" ? 0xf2c94c : 0xf4895a);
    g.circle(x + 2 * dir, by - 6.2, 0.7).fill(INK);
    if (b.kind === "parrot") g.roundRect(x - 2 - 3 * dir, by + 3, 3, 6, 1.2).fill(shade(b.c2, 1));
  }
}
