// The temporary helpers in the "Plano" office: vector figures (figure.ts) in neutral grey next to the desk of the employee who
// coordinates them. Where they stand comes from helperFigures.ts, the same plan as the 3D styles. They are not agents: a click selects
// the OWNER, they are not in the labels, the counters or the simulation. With no active group (or the flag off) update() only reads
// the store. With reduced motion, or on a machine that draws in software (officeQuality.ts), only a "N ayudantes" label stands over the desk.
import type * as PIXI from "pixi.js";
import { useHelpers } from "@/lib/helpers";
import { isActiveGroup } from "@/lib/helpersLogic";
import { useStore } from "@/lib/store";
import { useOfficeLayout } from "@/lib/officeLayout";
import { depthOf, humanSkin, iso, STAND_HIP, type Pose2 } from "./isoMath";
import { drawPerson, lookOf } from "./figure";
import { hexToNum } from "./isoMath";
import { FAIL_RED, HELPER_GREY, HELPER_HAIRS, HELPER_SKINS, LEAVE_S, hashKey, planHelpers, presence, type Fig, type Plan } from "../helperFigures";
import type { PeopleHost } from "./flatPeople";

const FONT = "IBM Plex Sans, Inter, system-ui, sans-serif";
const STYLES = ["short", "bob", "short", "bun", "ponytail"];

export interface HelperTexts { name(f: Fig): string; more(n: number): string; label(n: number): string }
export interface Helpers { update(o: { clock: number; reduce: boolean; scale: number; figScale: number; labelOnly?: boolean }): void; destroy(): void }

interface Item { fig: Fig; g: PIXI.Graphics; born: number; gone: number | null; look: ReturnType<typeof lookOf> }
interface Pill { c: PIXI.Container; bg: PIXI.Graphics; text: PIXI.Text; sig: string }

/** Reading: the arms up in front, holding the papers; a little nod. Failed or waiting: standing. */
const readingPose = (t: number, phase: number, working: boolean): Pose2 => {
  const b = Math.sin(t * 1.7 + phase);
  return working
    ? { hipY: STAND_HIP, lean: 0.1 + b * 0.02, legA: [0.03, -0.03], knee: [0, 0], armA: [0.9, 0.8], elbow: [0.95, 1.0], head: 0.12 + b * 0.05, sx: 1, sy: 1, seated: false }
    : { hipY: STAND_HIP, lean: 0.02, legA: [0.03, -0.03], knee: [0, 0], armA: [0.08, 0.05], elbow: [0.18, 0.15], head: 0, sx: 1, sy: 1, seated: false };
};

export function createHelpers(host: PeopleHost, deskMap: () => Map<string, { pos: [number, number] }>, texts: () => HelperTexts): Helpers {
  const { P, world, ui } = host;
  const items = new Map<string, Item>();
  const pills = new Map<string, Pill>();
  let tip: Pill | null = null;
  let hover: string | null = null;
  let slots: Record<string, number> = {};
  let plan: Plan = { figures: [], more: [], labels: [], slotOf: {} };
  let lastGroups: unknown = null, lastDesks: unknown = null, lastLabel: boolean | null = null;

  const makePill = (): Pill => {
    const c = new P.Container();
    const bg = new P.Graphics();
    const text = new P.Text({ text: "", style: { fontFamily: FONT, fontSize: 11.5, fontWeight: "600", fill: 0x111827 }, resolution: 2 });
    c.addChild(bg, text);
    c.eventMode = "none";
    ui.addChild(c);
    return { c, bg, text, sig: "" };
  };
  const setPill = (p: Pill, s: string) => {
    if (p.sig === s) return;
    p.sig = s;
    p.text.text = s;
    const w = p.text.width + 18;
    p.text.position.set(9, 4);
    p.bg.clear().roundRect(0, 0, w, 22, 11).fill({ color: 0xffffff, alpha: 0.95 }).stroke({ width: 1, color: 0xd5dae2 });
    p.c.pivot.set(w / 2, 22);
  };
  const dropPill = (m: Map<string, Pill>, k: string) => { const p = m.get(k); if (p) { p.c.destroy({ children: true }); m.delete(k); } };

  const addItem = (f: Fig, now: number) => {
    const h = hashKey(f.key);
    const g = new P.Graphics();
    g.eventMode = "static"; g.cursor = "pointer";
    g.hitArea = new P.Rectangle(-16, -78, 32, 80);
    g.on("pointertap", (e: PIXI.FederatedPointerEvent) => { e.stopPropagation(); const it = items.get(f.key); if (it) useStore.getState().select(it.fig.owner); });
    g.on("pointerover", () => { hover = f.key; });
    g.on("pointerout", () => { if (hover === f.key) hover = null; });
    world.addChild(g);
    const look = lookOf(hexToNum(HELPER_GREY.body), humanSkin(HELPER_SKINS[h % HELPER_SKINS.length]), hexToNum(HELPER_HAIRS[(h >>> 3) % HELPER_HAIRS.length]), STYLES[(h >>> 5) % STYLES.length], "");
    items.set(f.key, { fig: f, g, born: now, gone: null, look });
  };
  const dropItem = (k: string) => { const it = items.get(k); if (it) { it.g.destroy(); items.delete(k); } if (hover === k) hover = null; };

  return {
    update({ clock, reduce, scale, figScale, labelOnly: softwareOnly }) {
      const labelOnly = reduce || !!softwareOnly; // one count label per owner, no figures
      const st = useHelpers.getState();
      const active = st.enabled && Object.values(st.groups).some(isActiveGroup) && !useOfficeLayout.getState().editing;
      if (!active && !items.size && !pills.size) return; // zero cost: nothing to draw
      const now = performance.now() / 1000;
      const desks = deskMap();
      if (st.groups !== lastGroups || desks !== lastDesks || labelOnly !== lastLabel || !active) {
        lastGroups = st.groups; lastDesks = desks; lastLabel = labelOnly;
        if (active) {
          const pos: Record<string, [number, number]> = {};
          for (const [id, d] of desks) pos[id] = d.pos;
          plan = planHelpers(Object.values(st.groups), pos, { labelOnly, prev: slots });
          slots = plan.slotOf;
        } else plan = { figures: [], more: [], labels: [], slotOf: {} };
      }
      const here = new Set(plan.figures.map((f) => f.key));
      for (const f of plan.figures) {
        const it = items.get(f.key);
        if (it) { it.fig = f; it.gone = null; } else addItem(f, now);
      }
      for (const [k, it] of [...items]) {
        if (here.has(k)) continue;
        if (it.gone === null) it.gone = now;
        if (now - it.gone > LEAVE_S) dropItem(k);
      }
      const T = texts();
      const kFit = host.fit().k;
      for (const it of items.values()) {
        const f = it.fig;
        const p = presence(now - it.born, it.gone === null ? null : now - it.gone);
        const working = f.state === "working";
        drawPerson(it.g, readingPose(clock, hashKey(f.key) % 628 / 100, working && !reduce), it.look, 1, false, { mug: false, badge: "", docs: working, selectedColor: null, t: clock });
        if (f.state === "failed") it.g.circle(0, -84, 3.6).fill(FAIL_RED);
        if (f.state === "queued") it.g.ellipse(0, 0, 19, 6.5).stroke({ width: 1.5, color: 0x8b95a5, alpha: 0.6 });
        const c = iso(f.x, f.z);
        it.g.position.set(c.x, c.y);
        it.g.scale.set(figScale * (0.4 + 0.6 * p));
        it.g.alpha = p * (f.state === "queued" ? 0.7 : 1);
        it.g.zIndex = depthOf(f.x, f.z) + 0.02;
      }
      // the one hover tag
      const hov = hover ? items.get(hover) : undefined;
      if (hov) {
        tip ??= makePill();
        setPill(tip, T.name(hov.fig));
        const sp = host.toScreen(hov.fig.x, hov.fig.z);
        tip.c.position.set(sp.x, sp.y - 78 * figScale * kFit - 6);
        tip.c.scale.set(scale);
        tip.c.visible = true;
      } else if (tip) tip.c.visible = false;
      // "+N" after the visible figures, or the plain count ("5 ayudantes") in the one-label mode
      const want = new Map<string, string>();
      for (const m of plan.more) want.set("m:" + m.groupId, T.more(m.n));
      for (const l of plan.labels) want.set("l:" + l.owner, T.label(l.n));
      const at = new Map<string, { x: number; z: number }>();
      for (const m of plan.more) at.set("m:" + m.groupId, m);
      for (const l of plan.labels) at.set("l:" + l.owner, l);
      for (const k of [...pills.keys()]) if (!want.has(k)) dropPill(pills, k);
      for (const [k, s] of want) {
        let pill = pills.get(k);
        if (!pill) { pill = makePill(); pills.set(k, pill); }
        setPill(pill, s);
        const a = at.get(k)!;
        const sp = host.toScreen(a.x, a.z, 2.6);
        pill.c.position.set(sp.x, sp.y);
        pill.c.scale.set(scale);
      }
    },
    destroy() {
      for (const k of [...items.keys()]) dropItem(k);
      for (const k of [...pills.keys()]) dropPill(pills, k);
      if (tip) { tip.c.destroy({ children: true }); tip = null; }
    },
  };
}
