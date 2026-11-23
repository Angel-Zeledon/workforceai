// Live signals of the office drawn in the "Plano" style with vector art: the consult / delegation lines between people,
// the folders that travel between desks, the coins that float over whoever finished a task and the shield over an agent
// whose read content tried to give it orders. The rules (which events, how long, caps, reduced motion) are the 3D
// office's own: handoffWatch / FolderPool / useRewards.floats / useShield.flashes. Only the drawing is repeated here.
import type * as PIXI from "pixi.js";
import { useStore } from "@/lib/store";
import { useRewards } from "@/lib/rewards";
import { SHIELD_FLASH_MS, useShield } from "@/lib/shield";
import { shieldOpacity } from "@/lib/shieldLogic";
import { LINK_KIND } from "../kinds";
import { FolderPool } from "../officeState";
import { watchHandoffs } from "../handoffWatch";
import { userPos } from "../zones";
import { hexToNum } from "./isoMath";
import { INK } from "./figure";

const LINK_TTL = 7000;
const MAX_FOLDERS = 4;
const FLIGHT_MS = 2600;
const COIN_MS = 2400;
const N = 26;

type XZ = [number, number];
export interface FxHost {
  ui: PIXI.Container;
  toScreen(x: number, z: number, h?: number): { x: number; y: number };
  /** where an agent stands now and how many screen pixels its head is above its feet; null when it is not drawn */
  agent(id: string): { x: number; z: number; up: number } | null;
  /** the desk of an agent (world x, z) */
  desk(id: string): XZ | null;
}

export interface Fx { update(o: { now: number; dtS: number; reduce: boolean; scale: number }): void; destroy(): void }

export function createFx(P: typeof PIXI, host: FxHost): Fx {
  const g = new P.Graphics();
  g.zIndex = 0.5;
  host.ui.addChild(g);
  const pool = new FolderPool(MAX_FOLDERS, 10, 2500);
  const started: number[] = new Array(MAX_FOLDERS).fill(-1);
  const ends = new Float32Array(MAX_FOLDERS * 4);
  const spin = new Float32Array(MAX_FOLDERS);
  let reduceNow = false;
  const unwatch = watchHandoffs((h) => {
    if (reduceNow) return;
    const S = useStore.getState();
    if (!S.agents[h.from] || !S.agents[h.to]) return;
    pool.enqueue(h, performance.now());
  });
  const coins = new Map<string, { c: PIXI.Container; amount: number }>();

  const endpoint = (id: string): { x: number; y: number } | null => {
    if (id === "user" || id === "all" || id === "system") { const u = userPos(); const p = host.toScreen(u[0], u[2], 1.5); return p; }
    const a = host.agent(id);
    if (!a) return null;
    const p = host.toScreen(a.x, a.z);
    return { x: p.x, y: p.y - a.up - 8 };
  };

  const drawLinks = (now: number) => {
    const links = useStore.getState().links;
    let shown = 0;
    for (let i = links.length - 1; i >= 0 && shown < 6; i--) {
      const l = links[i];
      if (l.chat || now - l.ts >= LINK_TTL) continue;
      shown++;
      const a = endpoint(l.from), b = endpoint(l.to);
      if (!a || !b) continue;
      const color = hexToNum((LINK_KIND[l.kind] || LINK_KIND.chat).color);
      const age = now - l.ts;
      const fade = age > LINK_TTL - 1500 ? Math.max(0, (LINK_TTL - age) / 1500) : 1;
      const lift = 28 + Math.hypot(b.x - a.x, b.y - a.y) * 0.18;
      const at = (t: number) => ({ x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t - Math.sin(t * Math.PI) * lift });
      g.moveTo(a.x, a.y);
      for (let s = 1; s < N; s++) { const p = at(s / (N - 1)); g.lineTo(p.x, p.y); }
      g.stroke({ width: 2, color, alpha: 0.85 * fade, cap: "round", join: "round" });
      const tp = Math.min(1, age / 1400);
      if (age < 1500) { const d = at(tp); g.circle(d.x, d.y, 4.5).fill(color).stroke({ width: 1.2, color: 0xffffff }); }
      g.circle(b.x, b.y, 3).fill({ color, alpha: 0.9 * fade });
    }
  };

  const drawFolders = (now: number, dtS: number) => {
    pool.pump(now, () => FLIGHT_MS);
    const S = useStore.getState();
    for (let i = 0; i < MAX_FOLDERS; i++) {
      const sl = pool.slots[i];
      if (!sl.busy) { started[i] = -1; continue; }
      if (started[i] !== sl.t0) {
        started[i] = sl.t0;
        const a = host.desk(sl.from), b = host.desk(sl.to);
        if (!S.agents[sl.from] || !S.agents[sl.to] || !a || !b) { sl.busy = false; continue; }
        ends[i * 4] = a[0]; ends[i * 4 + 1] = a[1] + 0.2; ends[i * 4 + 2] = b[0]; ends[i * 4 + 3] = b[1] + 0.2;
      }
      const u = Math.min(1, (now - sl.t0) / sl.dur);
      const k = u * u * (3 - 2 * u);
      const x = ends[i * 4] + (ends[i * 4 + 2] - ends[i * 4]) * k, z = ends[i * 4 + 1] + (ends[i * 4 + 3] - ends[i * 4 + 1]) * k;
      const h = 0.95 + Math.sin(k * Math.PI) * 0.9;
      spin[i] += dtS * 2.2;
      const sc = (u < 0.1 ? u / 0.1 : u > 0.9 ? (1 - u) / 0.1 : 1) * 0.6 + 0.4;
      const sh = host.toScreen(x, z);
      g.ellipse(sh.x, sh.y, 9 * sc, 3.5 * sc).fill({ color: 0x0f172a, alpha: 0.12 });
      const corner = (dx: number, dz: number, hh = 0) => { const c = Math.cos(spin[i]), s = Math.sin(spin[i]); const p = host.toScreen(x + (dx * c + dz * s) * sc * 1.3, z + (-dx * s + dz * c) * sc * 1.3, h + hh); return [p.x, p.y]; };
      const body = [corner(-0.17, -0.125), corner(0.17, -0.125), corner(0.17, 0.125), corner(-0.17, 0.125)].flat();
      g.poly(body).fill(0xe4bd6c).stroke({ width: 1.1, color: INK, alpha: 0.6, join: "round" });
      g.poly([corner(-0.14, -0.095, 0.01), corner(0.14, -0.095, 0.01), corner(0.14, 0.095, 0.01), corner(-0.14, 0.095, 0.01)].flat()).fill(0xfbf6e8);
      g.poly([corner(-0.17, -0.125), corner(-0.05, -0.125), corner(-0.05, -0.17), corner(-0.14, -0.17)].flat()).fill(0xd6a94f);
    }
  };

  const drawShields = (now: number, reduce: boolean, scale: number) => {
    for (const f of useShield.getState().flashes) {
      const a = host.agent(f.agent_id);
      if (!a) continue;
      const t = Math.min(1, (now - f.at) / SHIELD_FLASH_MS);
      if (t >= 1) continue;
      const o = shieldOpacity(t);
      const feet = host.toScreen(a.x, a.z);
      const pulse = reduce ? 1 : 1 + ((t * 3) % 1) * 0.9;
      const rx = 22 * scale * pulse, ry = rx * 0.5;
      g.ellipse(feet.x, feet.y, rx, ry).stroke({ width: 2.4, color: 0x2f80c8, alpha: o * (reduce ? 0.35 : 0.5 * (1 - ((t * 3) % 1))) });
      const pop = reduce ? 1 : t < 0.08 ? 0.6 + (t / 0.08) * 0.5 : 1.1 - Math.min(0.1, (t - 0.08) * 0.6);
      const bob = reduce ? 0 : Math.sin(t * Math.PI * 6) * 2;
      const cx = feet.x, cy = feet.y - a.up - 30 * scale + bob, s = 15 * scale * pop;
      const shape = (k: number) => {
        g.moveTo(cx, cy - s * k).quadraticCurveTo(cx + s * 0.55 * k, cy - s * 0.85 * k, cx + s * 0.8 * k, cy - s * 0.75 * k)
          .lineTo(cx + s * 0.8 * k, cy + s * 0.1 * k).quadraticCurveTo(cx + s * 0.7 * k, cy + s * 0.65 * k, cx, cy + s * k)
          .quadraticCurveTo(cx - s * 0.7 * k, cy + s * 0.65 * k, cx - s * 0.8 * k, cy + s * 0.1 * k).lineTo(cx - s * 0.8 * k, cy - s * 0.75 * k)
          .quadraticCurveTo(cx - s * 0.55 * k, cy - s * 0.85 * k, cx, cy - s * k).closePath();
      };
      shape(1); g.fill({ color: 0x1f5f99, alpha: o }); g.stroke({ width: 2, color: 0xe9f3fb, alpha: o, join: "round" });
      shape(0.72); g.fill({ color: 0x2f80c8, alpha: o });
      g.moveTo(cx - s * 0.35, cy + s * 0.02).lineTo(cx - s * 0.08, cy + s * 0.32).lineTo(cx + s * 0.4, cy - s * 0.3).stroke({ width: 2.6 * scale, color: 0xffffff, alpha: o, cap: "round", join: "round" });
    }
  };

  const drawCoins = (now: number, reduce: boolean, scale: number) => {
    const floats = useRewards.getState().floats;
    const live = new Set<string>();
    for (const f of floats) {
      const a = host.agent(f.agent_id);
      const t = Math.min(1, (now - f.at) / COIN_MS);
      if (!a || t >= 1) continue;
      live.add(f.id);
      let e = coins.get(f.id);
      if (!e) {
        const c = new P.Container();
        const bg = new P.Graphics();
        bg.roundRect(0, 0, 66, 26, 13).fill({ color: 0xfffaeb, alpha: 0.97 }).stroke({ width: 1.5, color: 0xd2a24a });
        bg.circle(14, 13, 8).fill(0xe8b53a).stroke({ width: 1.4, color: 0xa86208 });
        bg.circle(14, 13, 4.6).stroke({ width: 1, color: 0xf7dd8a });
        const tx = new P.Text({ text: `+${f.amount}`, style: { fontFamily: "IBM Plex Sans, Inter, system-ui, sans-serif", fontSize: 14, fontWeight: "700", fill: 0x6e4205 }, resolution: 2 });
        tx.position.set(28, 4.5);
        c.addChild(bg, tx);
        c.pivot.set(33, 26);
        c.zIndex = 5;
        host.ui.addChild(c);
        e = { c, amount: f.amount };
        coins.set(f.id, e);
      }
      const rise = reduce ? 0 : 38 * scale * (1 - (1 - t) * (1 - t));
      const p = host.toScreen(a.x, a.z);
      e.c.position.set(p.x, p.y - a.up - 24 * scale - rise);
      e.c.scale.set(scale);
      e.c.alpha = reduce ? (t < 0.8 ? 1 : (1 - t) * 5) : t < 0.7 ? 1 : (1 - t) / 0.3;
    }
    for (const [id, e] of coins) if (!live.has(id)) { e.c.destroy({ children: true }); coins.delete(id); }
  };

  return {
    update({ now, dtS, reduce, scale }) {
      reduceNow = reduce;
      g.clear();
      if (reduce) pool.clear();
      drawLinks(now);
      if (!reduce) drawFolders(now, dtS);
      drawShields(now, reduce, scale);
      drawCoins(now, reduce, scale);
    },
    destroy() {
      unwatch();
      pool.clear();
      for (const e of coins.values()) e.c.destroy({ children: true });
      coins.clear();
      g.destroy();
    },
  };
}
