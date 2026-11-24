// The people connected to the office (not the agents), drawn in the "Plano" style with vectors: the same data and the same
// interaction as presence/PeopleLayer in the 3D styles. Mine walks with WASD / the arrows or a double click on the floor and
// reports where it is (usePresence + the realtime socket); the others glide toward the position the server announces.
// Each one has a name tag with the status colour. Hidden while the office is edited.
import type * as PIXI from "pixi.js";
import { useOfficeLayout } from "@/lib/officeLayout";
import { usePresence } from "../../presence/store";
import { sendPresence } from "../../presence/tap";
import { zoneAt } from "../../presence/zoneAt";
import {
  SPAWN, clampToFloor, colorFor, createMoveThrottle, keyDirection, shouldPing, smoothToward, stepToward, type PStatus, type Pt,
} from "../../presence/logic";
import { depthOf, facing, humanSkin, iso, unIso } from "./isoMath";
import { drawPerson, lookOf } from "./figure";
import { hslToNum, personPose, sceneFromClient, softenColor } from "./flatPeopleLogic";

const SPEED = 3.2; // meters per second, as in PeopleLayer
const STATUS_COLOR: Record<PStatus, number> = { available: 0x2f9e6b, focus: 0xc0392b, meeting: 0xb7791f, away: 0x8b95a5 };
const SKINS = ["#e8c4a0", "#d9a77c", "#c68b63", "#f0cfb0", "#b9805a"];
const HAIRS = [0x2b2118, 0x4a3020, 0x6b4a2c, 0x1d1d22, 0x8a6a44];
const STYLES = ["short", "bob", "short", "bun", "ponytail"];
const FONT = "IBM Plex Sans, Inter, system-ui, sans-serif";

export interface PeopleHost {
  P: typeof PIXI;
  world: PIXI.Container;
  ui: PIXI.Container;
  canvas: HTMLCanvasElement;
  /** the fitted view: scale and where the world sits on the canvas */
  fit(): { k: number; x: number; y: number };
  toScreen(x: number, z: number, h?: number): { x: number; y: number };
}
export interface People { update(o: { dtS: number; clock: number; reduce: boolean; scale: number; figScale: number }): void; destroy(): void }

interface Dot { g: PIXI.Graphics; tag: PIXI.Container; bg: PIXI.Graphics; dot: PIXI.Graphics; name: PIXI.Text; sig: string; cur: Pt | null; heading: number; moving: boolean; t0: number }

const typing = (t: EventTarget | null) => {
  const el = t as HTMLElement | null;
  return !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
};

export function createPeople(host: PeopleHost): People {
  const { P, world, ui, canvas } = host;
  const dots = new Map<string, Dot>();
  void usePresence.getState().load(); // who is online (the 3D layer does the same when it mounts)
  const make = (): Dot => {
    const g = new P.Graphics();
    g.eventMode = "none";
    world.addChild(g);
    const tag = new P.Container();
    const bg = new P.Graphics(), dot = new P.Graphics();
    const name = new P.Text({ text: "", style: { fontFamily: FONT, fontSize: 11.5, fontWeight: "600", fill: 0x111827 }, resolution: 2 });
    tag.addChild(bg, dot, name);
    tag.eventMode = "none";
    ui.addChild(tag);
    return { g, tag, bg, dot, name, sig: "", cur: null, heading: 0, moving: false, t0: Math.random() * 6 };
  };
  const layoutTag = (d: Dot, text: string, status: PStatus) => {
    const sig = text + "|" + status;
    if (sig === d.sig) return;
    d.sig = sig;
    d.name.text = text;
    const w = 22 + d.name.width + 9;
    d.name.position.set(21, 4);
    d.bg.clear().roundRect(0, 0, w, 22, 11).fill({ color: 0xffffff, alpha: 0.95 }).stroke({ width: 1, color: 0xd5dae2 });
    d.dot.clear().circle(12, 11, 4).fill(STATUS_COLOR[status]);
    d.tag.pivot.set(w / 2, 22);
    d.tag.alpha = status === "away" ? 0.7 : 1;
  };

  // my own movement: the same inputs as PeopleLayer.Self
  const keys = new Set<string>();
  let target: Pt | null = null;
  let pos: Pt = { ...SPAWN };
  let seeded = false;
  let heading = 0;
  let lastPing = 0;
  const throttle = createMoveThrottle();
  const ping = () => { const now = Date.now(); if (shouldPing(lastPing, now)) { lastPing = now; sendPresence({ type: "presence.ping" }); } };
  const onDown = (e: KeyboardEvent) => {
    ping();
    if (typing(e.target) || e.ctrlKey || e.metaKey || e.altKey || useOfficeLayout.getState().editing) return;
    const k = e.key.toLowerCase();
    if (["w", "a", "s", "d", "arrowup", "arrowdown", "arrowleft", "arrowright"].includes(k)) { keys.add(k); target = null; }
  };
  const onUp = (e: KeyboardEvent) => { keys.delete(e.key.toLowerCase()); };
  const onBlur = () => keys.clear();
  const onDbl = (e: MouseEvent) => {
    ping();
    if (useOfficeLayout.getState().editing) return;
    const f = host.fit(), r = canvas.getBoundingClientRect();
    const s = sceneFromClient(e.clientX, e.clientY, r, f, f.k);
    target = clampToFloor(unIso(s.x, s.y));
  };
  window.addEventListener("keydown", onDown);
  window.addEventListener("keyup", onUp);
  window.addEventListener("blur", onBlur);
  window.addEventListener("pointerdown", ping);
  canvas.addEventListener("dblclick", onDbl);

  return {
    update({ dtS, clock, reduce, scale, figScale }) {
      const editing = useOfficeLayout.getState().editing;
      const st = usePresence.getState();
      const live = new Set<string>();
      if (!editing) {
        const dt = Math.min(dtS, 0.1);
        const kFit = host.fit().k;
        for (const [id, p] of Object.entries(st.people)) {
          const mine = id === st.selfId;
          live.add(id);
          let d = dots.get(id);
          if (!d) { d = make(); dots.set(id, d); }
          if (mine) {
            if (!seeded) { pos = { x: p.x, z: p.z }; seeded = true; }
            const dir = keyDirection(keys);
            let next = pos;
            d.moving = false;
            if (dir) { next = clampToFloor({ x: next.x + dir.x * SPEED * dt, z: next.z + dir.z * SPEED * dt }); heading = Math.atan2(dir.x, dir.z); d.moving = true; }
            else if (target) { const s = stepToward(next, target, SPEED, dt); next = s.pos; heading = s.heading; d.moving = true; if (s.arrived) target = null; }
            pos = next;
            d.cur = pos; d.heading = heading;
            const now = performance.now();
            const out = d.moving ? throttle.offer(next, now) : throttle.flush(now) ?? throttle.offer(next, now);
            if (out) { const zone = zoneAt(out.x, out.z); st.moveSelf(out, zone); sendPresence({ type: "presence.move", x: out.x, z: out.z, zone }); }
          } else {
            const goal = { x: p.x, z: p.z };
            if (!d.cur) d.cur = { ...goal };
            const prev = d.cur;
            d.cur = smoothToward(prev, goal, dt);
            const dx = d.cur.x - prev.x, dz = d.cur.z - prev.z;
            d.moving = Math.hypot(dx, dz) / Math.max(dt, 1e-3) > 0.25;
            if (d.moving) d.heading = Math.atan2(dx, dz);
          }
          const away = p.status === "away";
          const h = id.split("").reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7);
          const look = lookOf(softenColor(hslToNum(colorFor(id))), humanSkin(SKINS[h % SKINS.length]), HAIRS[(h >>> 3) % HAIRS.length], STYLES[(h >>> 5) % STYLES.length], "");
          const f = facing(d.heading);
          drawPerson(d.g, personPose(d.moving && !reduce, clock + d.t0), look, f.dir, f.back, { mug: false, badge: "", docs: false, selectedColor: null, t: clock });
          // the status ring on the floor, like the 3D figure's
          d.g.ellipse(0, 0, 21, 7.5).stroke({ width: 2, color: STATUS_COLOR[p.status], alpha: away ? 0.5 : 0.95 });
          const c = iso(d.cur.x, d.cur.z);
          d.g.position.set(c.x, c.y);
          d.g.scale.set(figScale);
          d.g.alpha = away ? 0.6 : 1;
          d.g.zIndex = depthOf(d.cur.x, d.cur.z) + 0.02;
          d.g.visible = true;
          layoutTag(d, p.name, p.status);
          const sp = host.toScreen(d.cur.x, d.cur.z);
          d.tag.position.set(sp.x, sp.y - 74 * figScale * kFit - 6);
          d.tag.scale.set(scale);
          d.tag.visible = true;
        }
      }
      for (const [id, d] of dots) {
        if (live.has(id)) continue;
        if (editing) { d.g.visible = false; d.tag.visible = false; continue; } // kept for when editing ends
        d.g.destroy(); d.tag.destroy({ children: true }); dots.delete(id);
      }
    },
    destroy() {
      window.removeEventListener("keydown", onDown);
      window.removeEventListener("keyup", onUp);
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("pointerdown", ping);
      canvas.removeEventListener("dblclick", onDbl);
      for (const d of dots.values()) { d.g.destroy(); d.tag.destroy({ children: true }); }
      dots.clear();
    },
  };
}
