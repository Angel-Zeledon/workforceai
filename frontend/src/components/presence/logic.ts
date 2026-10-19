/**
 * Pure logic of human presence (no React, no three, no imports): proximity with hysteresis, the throttle of position
 * updates, walking, statuses and the activity ping. Mirrors backend/internal/presence/logic.go (same radii and rules)
 * and is unit tested with `node --test` (logic.test.mjs).
 */
export type PStatus = "available" | "focus" | "meeting" | "away";
export const STATUSES: PStatus[] = ["available", "focus", "meeting", "away"];
export const isStatus = (s: unknown): s is PStatus => typeof s === "string" && (STATUSES as string[]).includes(s);

/** Who may be offered a conversation: only people who are available (focus = do not disturb). */
export const approachable = (s: PStatus) => s === "available";

/** Same walkable slab as FLOOR in lib/officeGeometry.ts (kept as a copy to stay import-free). */
export const WALK_FLOOR = { x0: -13.9, x1: 13.9, z0: -9.7, z1: 9.7 };
export const ENTER_R = 2.5;
export const EXIT_R = 3.2;
/** Where a person appears when they come online (the entrance). */
export const SPAWN = { x: 0, z: 6 };

export interface Pt { x: number; z: number }
export interface Body extends Pt { status: PStatus }

export const dist = (a: Pt, b: Pt) => Math.hypot(a.x - b.x, a.z - b.z);
const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

export function clampToFloor(p: Pt): Pt {
  return { x: clamp(p.x, WALK_FLOOR.x0, WALK_FLOOR.x1), z: clamp(p.z, WALK_FLOOR.z0, WALK_FLOOR.z1) };
}

/** Near with hysteresis: become near inside `enter`, stop being near beyond `exit` (no flicker on the border). */
export function nextNear(wasNear: boolean, d: number, enter = ENTER_R, exit = EXIT_R): boolean {
  return wasNear ? d <= exit : d <= enter;
}

export interface NearInfo { id: string; distance: number; canTalk: boolean }

/**
 * The people near `self`, nearest first. `prev` is the set of ids that were near on the previous call (hysteresis).
 * canTalk is false when either side is not approachable (focus, meeting, away): the UI then shows no offer.
 */
export function nearby(self: Body, others: Record<string, Body>, prev: ReadonlySet<string>, enter = ENTER_R, exit = EXIT_R): NearInfo[] {
  const out: NearInfo[] = [];
  for (const [id, o] of Object.entries(others)) {
    const d = dist(self, o);
    if (!nextNear(prev.has(id), d, enter, exit)) continue;
    out.push({ id, distance: d, canTalk: approachable(self.status) && approachable(o.status) });
  }
  return out.sort((a, b) => a.distance - b.distance || (a.id < b.id ? -1 : 1));
}

export interface ThrottleOpts { minIntervalMs: number; minDelta: number }
export const MOVE_THROTTLE: ThrottleOpts = { minIntervalMs: 120, minDelta: 0.06 };

/**
 * Throttle of position updates sent to the server: `offer` returns the position to send now or null (held back);
 * `flush` releases a held position once the interval has passed (call it from a timer so the final resting place is
 * always sent). Time is injected, so it is deterministic in tests.
 */
export function createMoveThrottle(opts: ThrottleOpts = MOVE_THROTTLE) {
  let sent: (Pt & { at: number }) | null = null;
  let held: Pt | null = null;
  const send = (p: Pt, now: number): Pt => { sent = { ...p, at: now }; held = null; return p; };
  return {
    offer(p: Pt, now: number): Pt | null {
      if (!sent) return send(p, now);
      if (now - sent.at < opts.minIntervalMs) { if (dist(sent, p) >= opts.minDelta) held = p; return null; }
      if (dist(sent, p) < opts.minDelta) { held = null; return null; }
      return send(p, now);
    },
    flush(now: number): Pt | null {
      if (!held || !sent || now - sent.at < opts.minIntervalMs) return null;
      return send(held, now);
    },
    pending: () => held !== null,
  };
}

/** One walking step toward `target` at `speed` m/s for `dt` seconds. */
export function stepToward(cur: Pt, target: Pt, speed: number, dt: number): { pos: Pt; arrived: boolean; heading: number } {
  const dx = target.x - cur.x, dz = target.z - cur.z;
  const d = Math.hypot(dx, dz);
  const reach = speed * dt;
  if (d <= reach || d < 1e-6) return { pos: { ...target }, arrived: true, heading: d < 1e-6 ? 0 : Math.atan2(dx, dz) };
  return { pos: { x: cur.x + (dx / d) * reach, z: cur.z + (dz / d) * reach }, arrived: false, heading: Math.atan2(dx, dz) };
}

/** Direction of a WASD / arrow key set, normalised (diagonals are not faster). Null when nothing is pressed. */
export function keyDirection(keys: ReadonlySet<string>): Pt | null {
  const k = (...n: string[]) => n.some((x) => keys.has(x));
  const x = (k("d", "arrowright") ? 1 : 0) - (k("a", "arrowleft") ? 1 : 0);
  const z = (k("s", "arrowdown") ? 1 : 0) - (k("w", "arrowup") ? 1 : 0);
  if (!x && !z) return null;
  const n = Math.hypot(x, z);
  return { x: x / n, z: z / n };
}

/** Activity ping to the server (keeps auto-away at bay): at most once per interval. */
export const shouldPing = (lastMs: number, nowMs: number, intervalMs = 30_000) => nowMs - lastMs >= intervalMs;

/** Interpolation of a remote avatar toward the last position it announced (exponential smoothing, frame-rate independent). */
export function smoothToward(cur: Pt, target: Pt, dt: number, rate = 10): Pt {
  const k = 1 - Math.exp(-rate * dt);
  return { x: cur.x + (target.x - cur.x) * k, z: cur.z + (target.z - cur.z) * k };
}

/** Initials for an avatar badge. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  return (parts.length === 1 ? parts[0].slice(0, 2) : parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

/** Stable pastel-ish colour per person id (so the same person always looks the same to everyone). */
export function colorFor(id: string): string {
  let h = 2166136261;
  for (let i = 0; i < id.length; i++) { h ^= id.charCodeAt(i); h = Math.imul(h, 16777619); }
  return `hsl(${(h >>> 0) % 360}, 55%, 48%)`; // comma form: valid for CSS and for THREE.Color
}
