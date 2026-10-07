"use client";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { USER_POS } from "@/lib/meta";
import { headPos } from "./registry";
import { labelEntries } from "./labels";

interface Rect { x: number; y: number; w: number; h: number }
const hit = (a: Rect, b: Rect) => a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;
const v = new THREE.Vector3();
const cur = new Map<string, { x: number; y: number }>();

interface Item { key: string; el: HTMLElement; kind: string; prio: number; sx: number; sy: number; dist: number; ok: boolean }

/**
 * Anti-colisión en espacio de pantalla: proyecta cada ancla 3D, ordena por prioridad
 * (seleccionado/hover > globos > cercanos) y busca un hueco libre apilando hacia arriba.
 * Si no cabe la píldora completa baja a un avatar mini; si tampoco, se oculta (excepto la expandida).
 */
export function LabelSolver() {
  const { camera, size } = useThree();
  useFrame(() => {
    const items: Item[] = [];
    const now = Date.now();
    labelEntries.forEach((e, key) => {
      let wx: number, wy: number, wz: number;
      if (e.anchor === "user") { wx = USER_POS[0]; wy = 1.7; wz = USER_POS[2] - 0.2; }
      else {
        const h = headPos.get(e.anchor);
        if (!h) return;
        wx = h.x; wy = h.y + 0.5; wz = h.z;
      }
      v.set(wx, wy, wz);
      const dist = camera.position.distanceTo(v);
      v.project(camera);
      const ok = v.z < 1 && Math.abs(v.x) < 1.15 && Math.abs(v.y) < 1.15;
      const exp = e.el.dataset.exp === "1";
      items.push({
        key, el: e.el, kind: e.kind, ok, dist,
        sx: (v.x * 0.5 + 0.5) * size.width, sy: (-v.y * 0.5 + 0.5) * size.height,
        prio: exp ? 100 : e.kind === "bubble" ? 50 : 10 - dist * 0.01,
      });
    });
    items.sort((a, b) => b.prio - a.prio);
    const placed: Rect[] = [];
    for (const it of items) {
      const el = it.el;
      const scale = THREE.MathUtils.clamp(19 / it.dist, 0.72, 1.05);
      const exp = el.dataset.exp === "1";
      const isBubble = it.kind === "bubble";
      let tiers: { name: string; w: number; h: number }[];
      if (isBubble) tiers = [{ name: "bubble", w: 190, h: Number(el.dataset.h || 60) + 10 }];
      else if (exp) tiers = [{ name: "full", w: 206, h: 112 }];
      else tiers = [{ name: "full", w: Number(el.dataset.fw || 120), h: 30 }, { name: "mini", w: 30, h: 30 }];
      const lift = isBubble ? 54 * scale : 0;
      let done = false;
      let tx = it.sx, ty = it.sy, tier = "hidden";
      if (it.ok) {
        for (const t of tiers) {
          const w = t.w * scale, h = t.h * scale;
          for (let slot = 0; slot < 4 && !done; slot++) {
            const bottom = it.sy - lift - slot * (h + 4);
            const r = { x: it.sx - w / 2, y: bottom - h, w, h };
            if (placed.every((p) => !hit(p, r))) { placed.push(r); tx = it.sx; ty = bottom; tier = t.name; done = true; }
          }
          if (done) break;
        }
        if (!done && (exp || isBubble)) {
          const t = tiers[0]; const w = t.w * scale, h = t.h * scale;
          tx = it.sx; ty = it.sy - lift; tier = t.name; placed.push({ x: tx - w / 2, y: ty - h, w, h });
        }
      }
      let c = cur.get(it.key);
      if (!c) { c = { x: tx, y: ty }; cur.set(it.key, c); }
      c.x += (tx - c.x) * 0.3; c.y += (ty - c.y) * 0.3;
      el.style.transform = `translate(${c.x.toFixed(1)}px, ${c.y.toFixed(1)}px) translate(-50%, -100%) scale(${scale.toFixed(3)})`;
      if (!isBubble && el.dataset.tier !== tier) el.dataset.tier = tier;
      let op = tier === "hidden" ? 0 : it.dist > 24 ? 0.8 : 1;
      if (isBubble) {
        const age = now - Number(el.dataset.ts || now);
        op = tier === "hidden" ? 0 : age > 5500 ? Math.max(0, (7000 - age) / 1500) : 1;
      }
      el.style.opacity = String(op);
      el.style.zIndex = exp ? "1000" : isBubble ? "500" : String(10 + Math.round(it.sy / 10));
    }
    if (cur.size > labelEntries.size + 8) cur.forEach((_, k) => { if (!labelEntries.has(k)) cur.delete(k); });
  });
  return null;
}
