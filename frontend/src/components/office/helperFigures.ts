// Pure placement of the temporary-helper figures in the office (docs/superpowers/specs/2026-11-23-ayudantes-temporales-design.md §4).
// No React / three / path aliases: it runs under `node --test` (helperFigures.test.mjs). The renderers of the five styles only draw
// what this plans: where each helper stands (next to its owner's desk, never on another desk, the walkway or a wall), how many
// are visible (6 per group, 12 in all) and the "+N" that stands for the rest.

export interface Pt { x: number; z: number }
export interface PartIn { index: number; label: string; status?: string }
export interface GroupIn { id: string; owner_agent_id: string; status: string; parts: PartIn[] }
export type FigState = "working" | "queued" | "failed";

/** The walkable slab and the central walkway (copies of officeGeometry: this file imports nothing). */
export const ROOM = { x0: -13.9, x1: 13.9, z0: -9.7, z1: 9.7 };
const WALK = { x0: -14.5, x1: 14.5, z0: -2.15, z1: -0.45 };
/** Desk footprint (officeGeometry.deskBox): 2.4 x 2.3 centred 0.6 behind the desk point. */
const HW = 1.2, HD = 1.15, DZ = 0.6;
export const MAX_PER_GROUP = 6;
export const MAX_TOTAL = 12;
export const WALL_MARGIN = 0.45;
export const MIN_GAP = 0.8; // between two helpers

const boxDist = (p: Pt, d: [number, number]) => Math.hypot(Math.max(Math.abs(p.x - d[0]) - HW, 0), Math.max(Math.abs(p.z - (d[1] + DZ)) - HD, 0));
const inWalkway = (p: Pt) => p.x > WALK.x0 && p.x < WALK.x1 && p.z > WALK.z0 && p.z < WALK.z1;

export interface Slot extends Pt { /** heading (rad, about Y) that looks at the desk */ face: number }

/** Where up to `max` helpers of the owner standing at `desk` can stand: beside the desk, outside every desk, inside the room. */
export function slotsFor(desk: [number, number], desks: [number, number][], max = MAX_TOTAL): Slot[] {
  const others = desks.filter((d) => d[0] !== desk[0] || d[1] !== desk[1]);
  const cx = desk[0], cz = desk[1] + DZ;
  const cand: { p: Pt; score: number }[] = [];
  for (let x = cx - HW - 2.4; x <= cx + HW + 2.4 + 1e-9; x += 0.25) {
    for (let z = cz - HD - 2.4; z <= cz + HD + 2.4 + 1e-9; z += 0.25) {
      const p = { x: Math.round(x * 100) / 100, z: Math.round(z * 100) / 100 };
      if (p.x < ROOM.x0 + WALL_MARGIN || p.x > ROOM.x1 - WALL_MARGIN || p.z < ROOM.z0 + WALL_MARGIN || p.z > ROOM.z1 - WALL_MARGIN) continue;
      const own = boxDist(p, desk);
      if (own < 0.6 || own > 2.4 || inWalkway(p)) continue;
      if (others.some((o) => boxDist(p, o) < own + 0.9 || boxDist(p, o) < 0.6)) continue; // closer to somebody else's desk, or on it
      const side = Math.abs(p.x - cx) > HW ? (p.x > cx ? 0 : 0.15) : p.z > cz ? 0.5 : 0.9; // right of the desk first, then left, front, back
      cand.push({ p, score: own + side + Math.abs(p.z - cz) * 0.05 });
    }
  }
  cand.sort((a, b) => a.score - b.score || a.p.x - b.p.x || a.p.z - b.p.z);
  const out: Slot[] = [];
  for (const c of cand) {
    if (out.length >= max) break;
    if (out.some((s) => Math.hypot(s.x - c.p.x, s.z - c.p.z) < MIN_GAP)) continue;
    out.push({ ...c.p, face: Math.atan2(cx - c.p.x, cz - c.p.z) });
  }
  return out;
}

export interface Fig { key: string; groupId: string; owner: string; /** the part number (1-based): "Ayudante 3" */ n: number; label: string; state: FigState; slot: number; x: number; z: number; face: number }
export interface More { owner: string; groupId: string; /** the ones that do not fit */ n: number; x: number; z: number }
export interface Plan {
  figures: Fig[];
  /** "+N" after the visible figures of a group (full mode) */
  more: More[];
  /** the whole count per owner, in the one-label mode (software rendering / reduced motion) */
  labels: { owner: string; n: number; x: number; z: number }[];
  /** key -> slot, to hand back as `prev` so a helper keeps its place while the others come and go */
  slotOf: Record<string, number>;
}

const live = (g: GroupIn) => g.status === "running" || g.status === "joining";
const stateOf = (s?: string): FigState | null => (s === "done" || s === "stopped" ? null : s === "failed" ? "failed" : s === "running" ? "working" : "queued");
export const figKey = (groupId: string, index: number) => `${groupId}:${index}`;

/**
 * The plan for the groups on screen. Only running / joining groups count. Up to 6 figures per group and 12 in all (running first, then
 * failed, then waiting); a helper that finished is gone. `labelOnly`: no figure, one "+N" label per owner with everything still open.
 */
export function planHelpers(groups: GroupIn[], desks: Record<string, [number, number]>, o: { labelOnly?: boolean; prev?: Record<string, number> } = {}): Plan {
  const plan: Plan = { figures: [], more: [], labels: [], slotOf: {} };
  const list = groups.filter((g) => live(g) && desks[g.owner_agent_id]).sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  if (!list.length) return plan;
  const all = Object.values(desks);
  const slots = new Map<string, Slot[]>();
  const slotsOf = (owner: string) => { let s = slots.get(owner); if (!s) { s = slotsFor(desks[owner], all); slots.set(owner, s); } return s; };
  const taken = new Map<string, Set<number>>();
  let budget = MAX_TOTAL;
  const labelN = new Map<string, number>();
  for (const g of list) {
    const parts = g.parts.map((p) => ({ p, st: stateOf(p.status) })).filter((x): x is { p: PartIn; st: FigState } => x.st !== null);
    if (o.labelOnly) { labelN.set(g.owner_agent_id, (labelN.get(g.owner_agent_id) ?? 0) + parts.length); continue; }
    const rank = { working: 0, failed: 1, queued: 2 } as const;
    parts.sort((a, b) => rank[a.st] - rank[b.st] || a.p.index - b.p.index);
    const owner = g.owner_agent_id, sl = slotsOf(owner);
    const used = taken.get(owner) ?? new Set<number>();
    taken.set(owner, used);
    const show = parts.slice(0, Math.max(0, Math.min(MAX_PER_GROUP, budget, sl.length - used.size)));
    // the helpers that were already standing keep their slot
    const fresh: typeof show = [];
    for (const x of show) {
      const key = figKey(g.id, x.p.index), prev = o.prev?.[key];
      if (prev !== undefined && prev < sl.length && !used.has(prev)) { used.add(prev); plan.slotOf[key] = prev; } else fresh.push(x);
    }
    for (const x of fresh) {
      let i = 0;
      while (used.has(i)) i++;
      used.add(i);
      plan.slotOf[figKey(g.id, x.p.index)] = i;
    }
    for (const x of show) {
      const key = figKey(g.id, x.p.index), i = plan.slotOf[key], s = sl[i];
      plan.figures.push({ key, groupId: g.id, owner, n: x.p.index, label: x.p.label, state: x.st, slot: i, x: s.x, z: s.z, face: s.face });
    }
    budget -= show.length;
    const rest = parts.length - show.length;
    if (rest > 0) { const d = desks[owner]; plan.more.push({ owner, groupId: g.id, n: rest, x: d[0], z: d[1] + DZ }); }
  }
  for (const [owner, n] of labelN) { const d = desks[owner]; if (n > 0) plan.labels.push({ owner, n, x: d[0], z: d[1] + DZ }); }
  return plan;
}

/** Looks. Clothes are always the same neutral grey; the skin is a normal human tone, varied by the helper's key. */
export const HELPER_SKINS = ["#f0cfb0", "#e8c4a0", "#d9a77c", "#c68b63", "#b9805a", "#8d5a3b", "#f3d9bf", "#a86f4c"];
export const HELPER_HAIRS = ["#2b2118", "#4a3020", "#6b4a2c", "#1d1d22", "#8a6a44", "#3a2a1e"];
export const HELPER_GREY = { shirt: "#aab0b8", jacket: "#8d949e", knit: "#9aa1ab", pants: "#626972", belt: "#3b4048", body: "#8d949e" };
export const FAIL_RED = "#d64545";
export function hashKey(s: string): number {
  let h = 7;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h;
}

export const APPEAR_S = 0.8;
export const LEAVE_S = 0.9;
/** How present a figure is (0 absent .. 1 fully there): it eases in `APPEAR_S` after it comes, and out `LEAVE_S` after it is told to go. */
export function presence(age: number, goneFor: number | null): number {
  const ease = (t: number) => { const c = Math.min(1, Math.max(0, t)); return c * c * (3 - 2 * c); };
  const inn = ease(age / APPEAR_S);
  return goneFor === null ? inn : Math.min(inn, 1 - ease(goneFor / LEAVE_S));
}
