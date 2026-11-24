// The "Plano" office: PixiJS v8 in illustrated isometric (hand projection, vector furniture with ink outline, side-on
// figures with squash and stretch), drawn from the REAL layout, catalog and agents. pixi.js is loaded here and only here.
// Lifecycle: mountFlatScene() builds the app; the handle pauses/resumes it and destroys everything (no leaks).
import type * as PIXI from "pixi.js";
import { useStore, type Link } from "@/lib/store";
import { useRewards, type ShopItem } from "@/lib/rewards";
import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { usePreferences, getAgentView } from "@/lib/preferences";
import { useCost } from "@/lib/cost";
import { useProjects } from "@/lib/projects/store";
import { fmtUsd } from "@/lib/i18n";
import { stateMeta } from "@/lib/meta";
import { FIXTURE_IDS, FIXTURES, LEGACY_SLOTS, ZONE_NAMES, fixtureWorld, restOn, zoneCenterOf } from "@/lib/officeLayoutModel";
import { LINK_KIND } from "../kinds";
import { getSignals } from "../officeSignals";
import { agentAlertPaused, energyFor, progressOf } from "../officeHooks";
import { etaShort } from "../officeState";
import { userPos } from "../zones";
import { BUBBLE_GAP_MS, BUBBLE_SHOW_MS, phaseFor, pickBubble, stepBubbles, truncateText, type Bubble, type BubbleState } from "../officeState";
import { deskOf, seatOf, standOf, visitorOf } from "../registry";
import { depthOf, facing, hexToNum, humanSkin, iso, mapPose, S } from "./isoMath";
import { INK, drawPerson, lookOf, type Extras } from "./figure";
import { deskHeightOf, drawChair, drawDesk, drawFixture, drawMat, drawMonitorBody, drawPile, drawPiece, drawRoom, isFlatPiece } from "./draw";
import { makeSim, stepAgent, type Own, type Sim } from "./agentSim";
import { wornLookOf } from "./wornLook";
import { eventGestures } from "./flatGestures";
import { createFx, type Fx } from "./flatFx";
import { createPeople, type People } from "./flatPeople";
import { createHelpers, type Helpers } from "./flatHelpers";
import { dropFeet, placePopups, publishFeet } from "./flatPopups";
import { createFlatEditor, type FlatEditor } from "./flatEditor";

const ACCENT = 0x0a66c2;
const FONT = "IBM Plex Sans, Inter, system-ui, sans-serif";
/** Figures are drawn at 40 px per meter in figure.ts; scaled to the scene's meter (and a touch larger so people read). */
const FS = (S / 40) * 1.1;
const LINK_TTL = 7000;

export interface Tr { (key: string, vars?: Record<string, string | number>): string }
export interface FlatHandle { setRunning(on: boolean): void; destroy(): void }

type XZ = [number, number];
interface Chip { c: PIXI.Container; bg: PIXI.Graphics; name: PIXI.Text; pill: PIXI.Text; sub: PIXI.Text; dot: PIXI.Graphics; bars: PIXI.Graphics; t1: PIXI.Text; t2: PIXI.Text; sig: string; w: number }
/** The progress / time left and the remaining budget of an agent on its chip (real data: officeHooks.progressOf / energyFor). */
interface ChipMeter { pct: number; eta: string; etaLines: string; energy: { f: number; color: number; text: string; short: string } | null; showEnergy: boolean }
interface Bub { c: PIXI.Container; bg: PIXI.Graphics; kind: PIXI.Text; body: PIXI.Text; sig: string; w: number; h: number }
interface Node {
  id: string; g: PIXI.Graphics; sim: Sim; own: Own; chip: Chip; seatKey: string;
  look: ReturnType<typeof lookOf>; view: ReturnType<typeof getAgentView>; viewAt: number; hipY: number; meter?: ChipMeter; meterAt: number;
}
interface DeskDyn { id: string; g: PIXI.Graphics; x: number; z: number; deskH: number }

/** Mounts the scene into `el`. `tr` translates with the current language (read at use time, so a language change shows at once). */
export async function mountFlatScene(el: HTMLElement, tr: () => Tr): Promise<FlatHandle> {
  const P = await import("pixi.js");
  const app = new P.Application();
  const size = () => ({ w: Math.max(80, el.clientWidth), h: Math.max(80, el.clientHeight) });
  const s0 = size();
  await app.init({ width: s0.w, height: s0.h, background: 0xffffff, antialias: true, resolution: Math.min(2, window.devicePixelRatio || 1), autoDensity: true });
  el.appendChild(app.canvas);
  app.canvas.style.display = "block";
  app.canvas.style.touchAction = "manipulation";
  app.ticker.maxFPS = 30;

  const world = new P.Container();
  world.sortableChildren = true;
  const ui = new P.Container();
  app.stage.addChild(world, ui);
  app.stage.eventMode = "static";
  app.stage.hitArea = app.screen;
  app.stage.on("pointertap", () => useStore.getState().select(null));

  // ---------------------------------------------------------------- fitting
  let k = 1;
  const fit = () => {
    const w = app.screen.width, h = app.screen.height;
    const left = iso(-14.6, 10.3).x, right = iso(14.6, -10.3).x, top = iso(-14.6, -10.3, 3).y, bottom = iso(14.6, 10.3, -0.3).y;
    k = Math.max(0.2, Math.min(w / (right - left + 36), h / (bottom - top + 36)));
    world.scale.set(k);
    world.position.set(w / 2 - ((left + right) / 2) * k, h / 2 - ((top + bottom) / 2) * k + 6);
  };
  /** World point -> canvas pixels (the UI lives outside the scaled world so labels keep their size). */
  const toScreen = (x: number, z: number, h = 0) => { const p = iso(x, z, h); return { x: world.position.x + p.x * k, y: world.position.y + p.y * k }; };
  const resizeObs = new ResizeObserver(() => { const s = size(); app.renderer.resize(s.w, s.h); fit(); });
  resizeObs.observe(el);
  fit();

  // ---------------------------------------------------------------- static scene (rebuilt when the layout, catalog or team change)
  let statics: PIXI.Graphics[] = [];
  let deskDyn: DeskDyn[] = [];
  let pile: { g: PIXI.Graphics; x: number; z: number; rot: number; y: number } | null = null;
  const nodes = new Map<string, Node>();
  const zoneChips = new Map<string, Chip>();
  let deskInfo = new Map<string, { own: Own; pos: XZ }>();
  const layer = (z: number) => { const g = new P.Graphics(); g.zIndex = z; world.addChild(g); statics.push(g); return g; };

  const chipFor = (): Chip => {
    const c = new P.Container();
    const bg = new P.Graphics(), dot = new P.Graphics();
    const mk = (size: number, weight: string, fill: number) => new P.Text({ text: "", style: { fontFamily: FONT, fontSize: size, fontWeight: weight as "500", fill }, resolution: 2 });
    const name = mk(12, "600", 0x111827), pill = mk(9, "700", 0x64748b), sub = mk(10.5, "500", 0x475569);
    const bars = new P.Graphics();
    const t1 = mk(9.5, "600", 0x475569), t2 = mk(9.5, "600", 0x475569);
    t1.visible = false; t2.visible = false;
    c.addChild(bg, dot, name, pill, sub, bars, t1, t2);
    ui.addChild(c);
    return { c, bg, name, pill, sub, dot, bars, t1, t2, sig: "", w: 0 };
  };
  const layoutChip = (ch: Chip, o: { name: string; stateLabel: string; color: number; sub: string; expanded: boolean; showName: boolean; selected: boolean; ring: number; meter?: ChipMeter }) => {
    const sig = JSON.stringify(o);
    if (sig === ch.sig) return;
    ch.sig = sig;
    ch.name.text = o.showName ? o.name : o.name.slice(0, 1).toUpperCase();
    ch.pill.text = o.stateLabel.toUpperCase();
    ch.pill.style.fill = o.color;
    ch.sub.text = o.sub;
    ch.sub.visible = !!o.sub;
    const pad = 7;
    ch.dot.clear().circle(pad + 5, 11, 4.5).fill(o.ring);
    ch.name.position.set(pad + 14, 4);
    const row1 = pad + 14 + ch.name.width + (o.stateLabel ? 8 + ch.pill.width : 0) + pad;
    ch.pill.position.set(pad + 14 + ch.name.width + 8, 7.5);
    ch.pill.visible = !!o.stateLabel;
    let w = row1, h = 22;
    const m = o.meter;
    ch.bars.clear(); ch.t1.visible = false; ch.t2.visible = false;
    if (o.expanded && o.sub) {
      ch.sub.style.wordWrap = true; ch.sub.style.wordWrapWidth = 176; ch.sub.position.set(pad + 2, 24);
      w = Math.max(row1, Math.min(200, ch.sub.width + pad * 2 + 4)); h = 28 + ch.sub.height + 4;
      if (m && (m.etaLines || m.energy)) w = Math.max(w, 196);
      if (m?.etaLines) { ch.t1.text = m.etaLines; ch.t1.style.fill = 0x475569; ch.t1.position.set(pad + 2, h - 2); ch.t1.visible = true; h += ch.t1.height + 1; }
      if (m?.energy) {
        const bw = w - pad * 2 - 4;
        ch.bars.roundRect(pad + 2, h + 1, bw, 5, 2.5).fill({ color: 0x94a3b8, alpha: 0.25 });
        ch.bars.roundRect(pad + 2, h + 1, Math.max(3, bw * m.energy.f), 5, 2.5).fill(m.energy.color);
        ch.t2.text = m.energy.text; ch.t2.style.fill = m.energy.color; ch.t2.position.set(pad + 2, h + 8); ch.t2.visible = true;
        h += 8 + ch.t2.height + 2;
      }
      h += 2;
    } else if (m && !o.expanded) {
      let x = row1 - pad;
      if (m.pct >= 0) {
        ch.bars.roundRect(x + 5, 9, 26, 4, 2).fill({ color: 0x94a3b8, alpha: 0.3 });
        ch.bars.roundRect(x + 5, 9, Math.max(3, 26 * m.pct / 100), 4, 2).fill(o.color);
        x += 36;
        if (m.eta) { ch.t1.text = m.eta; ch.t1.style.fill = 0x475569; ch.t1.position.set(x - 2, 5.5); ch.t1.visible = true; x += ch.t1.width; }
      }
      if (m.showEnergy && m.energy) {
        ch.bars.roundRect(x + 5, 9, 28, 4, 2).fill({ color: 0x94a3b8, alpha: 0.3 });
        ch.bars.roundRect(x + 5, 9, Math.max(3, 28 * m.energy.f), 4, 2).fill(m.energy.color);
        ch.t2.text = m.energy.short; ch.t2.style.fill = m.energy.color; ch.t2.position.set(x + 37, 5.5); ch.t2.visible = true;
        x += 37 + ch.t2.width;
      }
      w = x + pad;
    }
    ch.w = w;
    ch.bg.clear().roundRect(0, 0, w, h, o.expanded ? 10 : 9).fill({ color: 0xffffff, alpha: 0.96 }).stroke({ width: o.selected ? 1.6 : 1, color: o.selected ? o.ring : 0xcfd6e0 });
    ch.c.pivot.set(w / 2, 0);
  };
  const bubbleFor = (): Bub => {
    const c = new P.Container();
    const bg = new P.Graphics();
    const kind = new P.Text({ text: "", style: { fontFamily: FONT, fontSize: 9, fontWeight: "700", fill: ACCENT, letterSpacing: 0.6 }, resolution: 2 });
    const body = new P.Text({ text: "", style: { fontFamily: FONT, fontSize: 11.5, fontWeight: "500", fill: 0x1f2937, wordWrap: true, wordWrapWidth: 150, lineHeight: 14.5 }, resolution: 2 });
    c.addChild(bg, kind, body);
    c.visible = false;
    ui.addChild(c);
    return { c, bg, kind, body, sig: "", w: 0, h: 0 };
  };
  const layoutBubble = (b: Bub, text: string, label: string, color: number) => {
    const sig = `${text}|${label}|${color}`;
    if (sig === b.sig) return;
    b.sig = sig;
    b.kind.text = label.toUpperCase(); b.kind.style.fill = color;
    b.body.text = text;
    b.kind.position.set(12, 6);
    b.body.position.set(12, 19);
    const w = Math.max(b.body.width, b.kind.width) + 24, h = 19 + b.body.height + 8;
    b.w = w; b.h = h;
    b.bg.clear().roundRect(0, 0, w, h, 10).fill(0xffffff).stroke({ width: 1, color: INK, alpha: 0.5 });
    b.bg.rect(1, 8, 3, h - 16).fill(color);
    b.bg.poly([w / 2 - 6, h - 0.5, w / 2 + 6, h - 0.5, w / 2, h + 7]).fill(0xffffff);
    b.bg.moveTo(w / 2 - 6, h).lineTo(w / 2, h + 7).lineTo(w / 2 + 6, h).stroke({ width: 1, color: INK, alpha: 0.5 });
    b.c.pivot.set(w / 2, h + 7);
  };

  /** the figure's colors and what it wears (shop items equipped, same data as the 3D character) */
  const lookFor = (id: string, v: ReturnType<typeof getAgentView>) => {
    const R = useRewards.getState();
    return lookOf(hexToNum(v.color), humanSkin(v.skin), hexToNum(v.hair, 0x3b2a1e), v.hairStyle, v.accessory, wornLookOf(R.equip[id], R.catalog, v.color));
  };
  const catalogOf = () => new Map(useRewards.getState().catalog.map((i) => [i.id, i] as const));
  const rebuild = () => {
    for (const g of statics) g.destroy();
    statics = []; deskDyn = []; pile = null;
    const S0 = useStore.getState();
    const layout = effective(useOfficeLayout.getState());
    const byId = catalogOf();
    drawRoom(layer(-10000), layout);
    const mats = layer(-9000);
    for (const name of ZONE_NAMES) { const [cx, cz] = zoneCenterOf(layout, name); drawMat(mats, name, cx, cz); }
    // workstations: mat, desk, monitor, chair, and the live screen
    deskInfo = new Map();
    S0.agentOrder.forEach((id, i) => {
      const ag = S0.agents[id];
      if (!ag) return;
      const pos = deskOf({ id, role: ag.role }, i);
      deskInfo.set(id, { pos, own: { seat: seatOf(pos), stand: standOf(pos) } });
      const cfg = layout.desks[id];
      const deskItem = cfg?.desk ? byId.get(cfg.desk) : undefined, chairItem = cfg?.chair ? byId.get(cfg.chair) : undefined;
      const view = getAgentView(id);
      drawMat(mats, "desk", pos[0], pos[1]);
      const dg = layer(depthOf(pos[0], pos[1]));
      drawDesk(dg, pos[0], pos[1], deskItem, hexToNum(view.color));
      const deskH = deskHeightOf(deskItem);
      drawMonitorBody(dg, pos[0], pos[1], deskH);
      const dyn = layer(depthOf(pos[0], pos[1]) + 0.02);
      deskDyn.push({ id, g: dyn, x: pos[0], z: pos[1], deskH });
      const seat = seatOf(pos);
      drawChair(layer(depthOf(seat[0], seat[1])), layer(depthOf(seat[0], seat[1]) + 0.05), seat[0], seat[1], chairItem);
    });
    // the built-in decoration and the furniture of the three rooms, where the layout puts them
    const base = new Map<string, number>();
    for (const id of FIXTURE_IDS) {
      const w = fixtureWorld(layout, id);
      if (!w) continue;
      const rest = FIXTURES[id].level === "top" ? restOn(layout, id) : { on: null, y: 0 };
      let z = depthOf(w.x, w.z);
      if (rest.on) { const sw = fixtureWorld(layout, rest.on); if (sw) z = depthOf(sw.x, sw.z) + 0.02 + (base.get(rest.on) ?? 0); base.set(rest.on, (base.get(rest.on) ?? 0) + 0.001); }
      drawFixture(layer(z), id, w.x, w.z, w.rot, rest.y);
    }
    if (layout.fixtures && fixtureWorld(layout, "approvals_desk")) { const w = fixtureWorld(layout, "approvals_desk")!; pile = { g: layer(depthOf(w.x, w.z) + 0.03), x: w.x, z: w.z, rot: w.rot, y: 0 }; }
    // the organization's pieces (or the 12 original slots until a layout exists)
    const pieces: { item: ShopItem; x: number; z: number; rot: number; s: number; y?: number }[] = [];
    if (layout.seeded) {
      for (const p of layout.pieces) { const item = byId.get(p.item); if (item) pieces.push({ item, x: p.x, z: p.z, rot: p.rot, s: p.s, y: p.y }); }
    } else {
      for (const [slot, itemId] of Object.entries(useRewards.getState().office)) {
        const spot = LEGACY_SLOTS[slot], item = byId.get(itemId);
        if (spot && item) pieces.push({ item, x: spot.pos[0], z: spot.pos[2], rot: spot.rot ?? 0, s: 1, y: spot.pos[1] });
      }
    }
    for (const p of pieces) drawPiece(layer(isFlatPiece(p.item) ? -8000 : depthOf(p.x, p.z) + (p.y ?? 0) * 0.01), p.item, p);

    // people: one per real agent
    for (const id of Array.from(nodes.keys())) {
      if (S0.agents[id]) continue;
      const n = nodes.get(id)!; n.g.destroy(); n.chip.c.destroy({ children: true }); nodes.delete(id);
    }
    for (const id of S0.agentOrder) {
      const info = deskInfo.get(id);
      if (!info) continue;
      const have = nodes.get(id);
      if (have) {
        const key = `${info.own.seat[0].toFixed(2)},${info.own.seat[1].toFixed(2)}`;
        if (key !== have.seatKey) { have.seatKey = key; have.own = info.own; have.sim.place = ""; } // the desk moved: plan a new route
        continue;
      }
      const g = new P.Graphics();
      g.eventMode = "static"; g.cursor = "pointer";
      g.hitArea = new P.Rectangle(-19, -74, 38, 80);
      g.on("pointertap", (e: PIXI.FederatedPointerEvent) => { e.stopPropagation(); useStore.getState().select(id); });
      world.addChild(g);
      const chip = chipFor();
      chip.c.eventMode = "static"; chip.c.cursor = "pointer";
      chip.c.on("pointertap", (e: PIXI.FederatedPointerEvent) => { e.stopPropagation(); useStore.getState().select(id); });
      const view = getAgentView(id);
      nodes.set(id, {
        id, g, sim: makeSim(id, info.own.seat), own: info.own, chip, seatKey: `${info.own.seat[0].toFixed(2)},${info.own.seat[1].toFixed(2)}`,
        look: lookFor(id, view), view, viewAt: 0, hipY: 30, meterAt: 0,
      });
    }
  };

  // what changes the static scene: the team, the effective layout (draft or saved), the catalog and the legacy slots
  let sigKey: unknown[] = [];
  const staticSig = (): unknown[] => {
    const S0 = useStore.getState();
    return [S0.agentOrder.map((id) => `${id}:${S0.agents[id]?.role ?? ""}`).join(","), effective(useOfficeLayout.getState()), useRewards.getState().catalog, useRewards.getState().office];
  };
  const changed = (a: unknown[], b: unknown[]) => a.length !== b.length || a.some((v, i) => v !== b[i]);
  rebuild(); sigKey = staticSig();

  // ---------------------------------------------------------------- speech: the real content only (same rules as the 3D office)
  const bubState: BubbleState = new Map();
  let speech: { id: string; text: string; kind: string; shownAt: number }[] = [];
  let linkB: { id: string; anchor: string; text: string; label: string; color: number; ts: number }[] = [];
  const bubViews = new Map<string, Bub>();
  let bubAt = 0;
  const computeBubbles = (now: number) => {
    const T = tr();
    const prefs = usePreferences.getState().prefs;
    const S0 = useStore.getState();
    // chat / consult / delegation lines: one per sender, at most three
    const seen = new Set<string>();
    const out: Link[] = [];
    for (let i = S0.links.length - 1; i >= 0 && out.length < 3; i--) {
      const l = S0.links[i];
      if (now - l.ts > LINK_TTL || seen.has(l.from)) continue;
      seen.add(l.from); out.push(l);
    }
    linkB = out.map((l) => {
      const base = LINK_KIND[l.kind] || LINK_KIND.chat;
      const fromAgent = !["user", "all", "system"].includes(l.from);
      const first = l.text.split("\n")[0];
      return { id: l.id, anchor: fromAgent ? l.from : "user", text: first.length > 86 ? first.slice(0, 84) + "…" : first, label: fromAgent && l.chat ? (S0.agents[l.from]?.name ?? base.label).split(" ")[0] : base.label, color: hexToNum(base.color), ts: l.ts };
    });
    if (!prefs.showBubbles) { speech = []; bubState.clear(); return; }
    const sig = getSignals();
    const cycle = Math.floor(now / (BUBBLE_SHOW_MS + BUBBLE_GAP_MS));
    const talking = new Set<string>();
    for (const l of S0.links) if (now - l.ts < LINK_TTL) talking.add(l.from);
    const meeting: { id: string; bubble: Bubble | null }[] = [], rest: typeof meeting = [];
    for (const id of S0.agentOrder) {
      const ag = S0.agents[id];
      if (!ag) continue;
      if (sig.debate?.seats.includes(id)) {
        const d = sig.debate;
        meeting.push({ id, bubble: d.speaker === id && d.text ? { key: `debate:${d.id}:${d.turn}`, kind: "debate", text: truncateText(d.text, 90), live: true } : null });
        continue;
      }
      if (talking.has(id)) continue;
      let line: { text: string; kind: string; ageMs: number } | null = null;
      for (let i = S0.links.length - 1; i >= 0; i--) { const l = S0.links[i]; if (l.from === id && (l.kind === "chat" || l.kind === "consult")) { line = { text: l.text, kind: l.kind, ageMs: now - l.ts }; break; } }
      const slot = sig.meetingSlot.get(id);
      const phaseTitle = slot === undefined ? null : sig.phases.length ? phaseFor(sig.phases, slot, cycle) : slot === 0 && sig.planningText ? sig.planningText : null;
      const paused = sig.cafeSlot.has(id);
      const bubble = pickBubble({
        state: paused ? "working" : ag.state, activity: ag.activity || "", phaseTitle, line,
        taskTitle: paused ? "" : (ag.current_task_id ? S0.tasks[ag.current_task_id]?.title ?? "" : ""),
        file: S0.files[id] ? { action: S0.files[id].action, path: S0.files[id].path, ageMs: now - S0.files[id].ts } : null,
      });
      (slot !== undefined ? meeting : rest).push({ id, bubble: bubble && paused ? { ...bubble, urgent: true } : bubble });
    }
    const ids = stepBubbles(bubState, [...meeting, ...rest], now);
    speech = ids.map((id) => { const s = bubState.get(id)!; return { id, text: s.text, kind: s.kind, shownAt: s.shownAt }; });
    void T;
  };

  /** progress / time left and remaining budget of one agent (the 3D tags show the same, from the same functions) */
  const meterOf = (id: string, ag: NonNullable<ReturnType<typeof useStore.getState>["agents"][string]>, now: number, T: Tr): ChipMeter | undefined => {
    const S0 = useStore.getState(), C = useCost.getState(), Pj = useProjects.getState();
    const prog = progressOf(ag, ag.current_task_id ? S0.tasks[ag.current_task_id] : undefined, Pj.detail, Pj.health, now);
    const en = energyFor(C.status?.agents.find((a) => a.agent_id === id), agentAlertPaused(C.alerts, id));
    if (!prog && !en) return undefined;
    const col = en ? (en.level === "ok" ? 0x2f9e6b : en.level === "low" ? 0xd9890b : 0xd14343) : 0;
    const lines: string[] = [];
    if (prog) {
      lines.push([T("office.progress", { pct: Math.round(prog.eta.pct) }), prog.eta.etaS !== null ? T("office.eta", { time: etaShort(prog.eta.etaS) }) : ""].filter(Boolean).join("  "));
      if (prog.projectEtaS !== null) lines.push(T("office.eta.project", { time: etaShort(prog.projectEtaS) }));
    }
    if (en?.paused) lines.push(T("office.energy.paused"));
    return {
      pct: prog ? Math.round(prog.eta.pct) : -1, eta: prog && prog.eta.etaS !== null ? etaShort(prog.eta.etaS) : "", etaLines: lines.join("\n"),
      energy: en ? { f: en.fraction, color: col, text: T("office.energy.left", { amount: fmtUsd(en.remaining) }), short: fmtUsd(en.remaining) } : null,
      showEnergy: !!en && (en.paused || en.level !== "ok"),
    };
  };

  // ---------------------------------------------------------------- frame
  const headPx = (n: Node) => (n.hipY + 24 + 9 + 9 + 22) * FS;
  let sigAt = 0;
  const tick = (tk: PIXI.Ticker) => {
    const dt = Math.min(0.05, tk.deltaMS / 1000);
    const nowMs = Date.now();
    const T = tr();
    const prefs = usePreferences.getState().prefs;
    const reduce = prefs.reduceMotion;
    app.ticker.maxFPS = reduce ? 10 : 30;
    sigAt += dt;
    if (sigAt > 0.4) { sigAt = 0; const s = staticSig(); if (changed(s, sigKey)) { sigKey = s; rebuild(); } }
    if (nowMs - bubAt > 1000) { bubAt = nowMs; computeBubbles(nowMs); }
    const S0 = useStore.getState();
    const sig = getSignals();
    const layout = effective(useOfficeLayout.getState());
    const clock = reduce ? 0 : performance.now() / 1000;
    const chipScale = Math.max(0.8, Math.min(1.15, (k * S) / 26)) * (prefs.labelSize === "s" ? 0.85 : prefs.labelSize === "l" ? 1.2 : 1);

    // desks: the live screen (it wakes with the work, like the 3D monitor) and the pile of pending approvals
    for (const d of deskDyn) {
      const ag = S0.agents[d.id];
      d.g.clear();
      if (!ag) continue;
      const st = ag.state;
      const on = st === "working" || st === "thinking" || st === "reviewing" || st === "waiting" || st === "talking";
      const assigned = st === "idle" && Object.values(S0.tasks).some((tk) => tk.agent_id === d.id && (tk.status === "pending" || tk.status === "running"));
      const lvl = st === "working" ? 1 : on ? 0.85 : assigned ? 0.7 : st === "awaiting_approval" ? 0.75 : st === "error" || st === "blocked" ? 0.8 : 0.4;
      const col = st === "error" || st === "blocked" ? 0xf0b3ad : st === "awaiting_approval" ? 0xf1dba3 : 0xbfe0f7;
      const mz = d.z - 0.12 + 0.022, y0 = d.deskH + 0.26, y1 = d.deskH + 0.76, x0 = d.x - 0.43, x1 = d.x + 0.43;
      d.g.poly([iso(x0, mz, y0).x, iso(x0, mz, y0).y, iso(x1, mz, y0).x, iso(x1, mz, y0).y, iso(x1, mz, y1).x, iso(x1, mz, y1).y, iso(x0, mz, y1).x, iso(x0, mz, y1).y]).fill({ color: col, alpha: 0.35 + 0.65 * lvl });
      if (on || st === "error") {
        for (let i = 0; i < 5; i++) {
          const w = st === "working" && !reduce ? 0.35 + 0.65 * Math.abs(Math.sin(clock * (1.5 + i * 0.7) + i)) : 0.55 + 0.1 * Math.sin(i);
          const y = y1 - 0.08 - i * 0.075, xa = x0 + 0.08, xb = xa + 0.7 * w;
          d.g.poly([iso(xa, mz, y).x, iso(xa, mz, y).y, iso(xb, mz, y).x, iso(xb, mz, y).y, iso(xb, mz, y - 0.035).x, iso(xb, mz, y - 0.035).y, iso(xa, mz, y - 0.035).x, iso(xa, mz, y - 0.035).y]).fill({ color: i === 0 ? ACCENT : 0xffffff, alpha: 0.85 });
        }
      }
      if (ag.current_task_id && (st === "working" || st === "thinking" || st === "reviewing")) {
        const f = Math.max(0.03, Math.min(1, (ag.progress || 0) / 100)), y = y0 + 0.03, xb = x0 + 0.08 + 0.7 * f;
        d.g.poly([iso(x0 + 0.08, mz, y).x, iso(x0 + 0.08, mz, y).y, iso(xb, mz, y).x, iso(xb, mz, y).y, iso(xb, mz, y + 0.03).x, iso(xb, mz, y + 0.03).y, iso(x0 + 0.08, mz, y + 0.03).x, iso(x0 + 0.08, mz, y + 0.03).y]).fill(0x16a34a);
      }
    }
    if (pile) {
      const n = Object.values(S0.approvals).filter((a) => a.status === "pending").length;
      pile.g.clear();
      if (n) drawPile(pile.g, pile.x, pile.z, pile.rot, pile.y, n, reduce ? 0 : Math.abs(Math.sin(clock * 2.4)) * 0.05);
    }

    // the people
    for (const n of nodes.values()) {
      const ag = S0.agents[n.id];
      if (!ag) continue;
      const selected = S0.selectedAgentId === n.id;
      if (nowMs - n.viewAt > 600) {
        n.viewAt = nowMs;
        const v = getAgentView(n.id);
        n.view = v;
        n.look = lookFor(n.id, v);
      }
      const step = stepAgent(n.sim, n.id, dt, n.own, {
        selected, reduce,
        deskStand: (id) => deskInfo.get(id)?.pos ? standOf(deskInfo.get(id)!.pos) : null,
        deskVisit: (id) => deskInfo.get(id)?.pos ? visitorOf(deskInfo.get(id)!.pos) : null,
      });
      if (!step) continue;
      const sm = n.sim;
      publishFeet(n.id, sm.x, sm.z); // the approvals card reads who has reached the desk
      eventGestures(sm, n.id, step, reduce, (id) => { const o = nodes.get(id); return o ? [o.sim.x, o.sim.z] : null; });
      if (nowMs - n.meterAt > 1000) { n.meterAt = nowMs; n.meter = meterOf(n.id, ag, nowMs, T); }
      const p2 = mapPose(sm.pose);
      n.hipY = p2.hipY;
      const f = facing(sm.yaw);
      const st = ag.state;
      const badge: Extras["badge"] = step.walking ? "" : st === "completed" ? "check" : st === "blocked" || st === "error" ? (step.atZone === "cafe" ? "" : "alert") : st === "awaiting_approval" ? "warn" : st === "thinking" ? "think" : "";
      drawPerson(n.g, p2, n.look, f.dir, f.back, { mug: step.mug, badge, docs: st === "reviewing" && !step.walking, selectedColor: selected ? n.look.body : null, t: clock });
      const c = iso(sm.x, sm.z);
      n.g.position.set(c.x, c.y);
      n.g.scale.set(FS * (1 - step.sq * 0.6), FS * (1 + step.sq));
      n.g.zIndex = depthOf(sm.x, sm.z) + 0.02;

      // the name chip under the feet; the selected one opens up with the real activity
      const sm2 = stateMeta(st);
      const showName = prefs.showNames;
      const exp = selected;
      layoutChip(n.chip, {
        name: n.view.label || n.view.name, stateLabel: st === "idle" ? "" : sm2.label, color: hexToNum(sm2.color), ring: hexToNum(n.view.color),
        sub: exp ? [n.view.title, ag.activity ? truncateText(ag.activity, 110) : ""].filter(Boolean).join("\n") : "", expanded: exp, showName, selected, meter: n.meter,
      });
      const sp = toScreen(sm.x, sm.z);
      n.chip.c.position.set(sp.x, sp.y + 8 * FS * k);
      n.chip.c.scale.set(chipScale);
      n.chip.c.zIndex = exp ? 2 : 1;
    }

    // while the office is edited the pointer belongs to the editor (pick, drag, place): people do not catch it
    const editingNow = useOfficeLayout.getState().editing;
    for (const n of nodes.values()) { const m = editingNow ? "none" : "static"; if (n.g.eventMode !== m) { n.g.eventMode = m; n.chip.c.eventMode = m; } }
    editor.update();

    // consult / delegation lines, travelling folders, coins and shields
    fx.update({ now: nowMs, dtS: dt, reduce, scale: chipScale });
    people.update({ dtS: dt, clock, reduce, scale: chipScale, figScale: FS });
    helpers.update({ clock, reduce, scale: chipScale, figScale: FS });
    placePopups(toScreen, app.screen.width, app.screen.height);

    // speech bubbles (a message line wins over the working line of the same agent)
    const live = new Set<string>();
    const place = (key: string, anchor: string, text: string, label: string, color: number, since: number, ttl: number) => {
      const n = nodes.get(anchor);
      let x: number, y: number;
      if (n) { const sp = toScreen(n.sim.x, n.sim.z); x = sp.x; y = sp.y - headPx(n) * k - 6; }
      else if (anchor === "user") { const u = userPos(); const sp = toScreen(u[0], u[2]); x = sp.x; y = sp.y - 70 * FS * k; }
      else return;
      let b = bubViews.get(key);
      if (!b) { b = bubbleFor(); bubViews.set(key, b); }
      layoutBubble(b, text, label, color);
      const age = nowMs - since;
      const a = Math.min(1, age / 180) * Math.min(1, Math.max(0, (ttl - age) / 400 + 0.01));
      b.c.visible = a > 0.02; b.c.alpha = a;
      b.c.scale.set(chipScale * (0.85 + 0.15 * Math.min(1, age / 180)));
      b.c.position.set(Math.max(b.w / 2 * chipScale + 4, Math.min(app.screen.width - b.w / 2 * chipScale - 4, x)), Math.max(b.h * chipScale + 14, y));
      live.add(key);
    };
    for (const l of linkB) place(`l:${l.id}`, l.anchor, l.text, l.label, l.color, l.ts, LINK_TTL);
    for (const s of speech) {
      if (linkB.some((l) => l.anchor === s.id)) continue;
      const n = nodes.get(s.id);
      place(`s:${s.id}:${s.shownAt}`, s.id, s.text, T(`office.bubble.${s.kind}`), hexToNum(n?.view.color), s.shownAt, BUBBLE_SHOW_MS);
    }
    for (const [key, b] of bubViews) if (!live.has(key)) { b.c.destroy({ children: true }); bubViews.delete(key); }

    // the rooms' tags: the name of each room, how many approvals wait, and when the meeting room is in use
    const pending = Object.values(S0.approvals).filter((a) => a.status === "pending").length;
    const zl: Record<string, { text: string; at: XZ; color: number }> = {
      meeting: { text: sig.meetingSlot.size ? `${T("oe.zone.meeting")} · ${T(sig.debate ? "office.debate.title" : "office.planning.title")}` : T("oe.zone.meeting"), at: (() => { const c = zoneCenterOf(layout, "meeting"); return [c[0], c[1] + 3.1] as XZ; })(), color: sig.debate ? 0xc2410c : 0x5a7fd0 },
      approvals: { text: pending ? `${T("oe.zone.approvals")} · ${T("approvals.pendingTitle", { count: pending })}` : T("oe.zone.approvals"), at: (() => { const c = zoneCenterOf(layout, "approvals"); return [c[0], c[1] + 3.1] as XZ; })(), color: pending ? 0xb98a34 : 0x94a3b8 },
      cafe: { text: T("office.cafeteria"), at: (() => { const c = zoneCenterOf(layout, "cafe"); return [c[0] + 0.2, c[1] + 2.3] as XZ; })(), color: 0xc98a4a },
    };
    for (const [name, z] of Object.entries(zl)) {
      let ch = zoneChips.get(name);
      if (!ch) { ch = chipFor(); ch.c.eventMode = "none"; zoneChips.set(name, ch); }
      layoutChip(ch, { name: z.text, stateLabel: "", color: 0x64748b, ring: z.color, sub: "", expanded: false, showName: true, selected: false });
      ch.c.alpha = 0.92;
      const sp = toScreen(z.at[0], z.at[1]);
      ch.c.position.set(sp.x, sp.y);
      ch.c.scale.set(chipScale * 0.92);
    }
  };
  ui.sortableChildren = true;
  const fx: Fx = createFx(P, {
    ui, toScreen,
    agent: (id) => { const n = nodes.get(id); return n ? { x: n.sim.x, z: n.sim.z, up: headPx(n) * k } : null; },
    desk: (id) => deskInfo.get(id)?.pos ?? null,
  });
  const people: People = createPeople({ P, world, ui, canvas: app.canvas, fit: () => ({ k, x: world.position.x, y: world.position.y }), toScreen });
  // temporary helpers next to their owner's desk (reads the store only while there is a group; texts follow the language)
  const helpers: Helpers = createHelpers(
    { P, world, ui, canvas: app.canvas, fit: () => ({ k, x: world.position.x, y: world.position.y }), toScreen },
    () => deskInfo,
    () => { const T = tr(); return {
      name: (f) => T("helpers.fig.name", { n: f.n, part: f.label, state: T(`helpers.fig.${f.state}`) }),
      more: (n) => T("helpers.fig.more", { n }),
      label: (n) => (n === 1 ? T("helpers.fig.label1") : T("helpers.fig.label", { n })),
    }; },
  );
  const editor: FlatEditor = createFlatEditor({
    P, world, canvas: app.canvas, fit: () => ({ k, x: world.position.x, y: world.position.y }), requestRebuild: () => { sigAt = 1; },
  });
  app.ticker.add(tick);
  // development (or NEXT_PUBLIC_OFFICE_DEBUG) only, like window.__officeQuality in the 3D styles: stop the real loop and advance
  // the simulation by virtual time (deterministic walks for screenshots on a computer that renders WebGL in software)
  const wdbg = window as unknown as { __officeFlat?: unknown };
  if (process.env.NODE_ENV !== "production" || process.env.NEXT_PUBLIC_OFFICE_DEBUG === "true") {
    wdbg.__officeFlat = {
      freeze: (on: boolean) => { if (on) app.ticker.stop(); else app.ticker.start(); },
      step: (n: number, dt: number) => { for (let i = 0; i < n; i++) tick({ deltaMS: dt * 1000 } as PIXI.Ticker); app.render(); },
      /** page pixel (client coordinates) of the floor point (x, z) at height h */
      project: (x: number, z: number, h = 0) => { const s = toScreen(x, z, h), r = app.canvas.getBoundingClientRect(); return { x: r.left + s.x, y: r.top + s.y }; },
    };
  }

  let running = true;
  return {
    setRunning(on) {
      if (on === running) return;
      running = on;
      if (on) app.ticker.start(); else app.ticker.stop();
    },
    destroy() {
      running = false;
      delete wdbg.__officeFlat;
      resizeObs.disconnect();
      try { app.ticker.remove(tick); app.ticker.stop(); } catch { /* already gone */ }
      const gl = (app.renderer as { gl?: WebGLRenderingContext | WebGL2RenderingContext }).gl;
      try { app.destroy(true, { children: true, texture: true }); } catch { /* destroyed during init */ }
      try { gl?.getExtension("WEBGL_lose_context")?.loseContext(); } catch { /* not supported */ }
      editor.destroy(); fx.destroy(); people.destroy(); helpers.destroy(); dropFeet(nodes.keys());
      nodes.clear(); zoneChips.clear(); bubViews.clear(); statics = []; deskDyn = [];
    },
  };
}
