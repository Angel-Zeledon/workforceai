// Pure animation math for the office characters: base poses per (placement, state), a layer of one-shot
// gestures (fidgets and event reactions) and the seeded RNG that desynchronizes characters.
// No THREE / React imports: everything here is allocation-free per call and unit-testable (gestures.test.mjs).

export const KEYS = [
  "pelvisY", "torsoX", "torsoY", "torsoZ", "headX", "headY", "headZ",
  "lShX", "lShZ", "lElX", "rShX", "rShZ", "rElX",
  "lHipX", "rHipX", "lKneeX", "rKneeX",
] as const;
export type PoseKey = (typeof KEYS)[number];
export type Pose = Record<PoseKey, number>;

export const STAND_Y = 0.4;
export const SEAT_Y = 0.46;
export const WALK_SPEED = 2.1;
const TAU = Math.PI * 2;

export const zeroPose = (): Pose => {
  const p = {} as Pose;
  for (const k of KEYS) p[k] = 0;
  return p;
};
export const wrapAngle = (a: number) => Math.atan2(Math.sin(a), Math.cos(a));
export const clamp = (v: number, lo: number, hi: number) => (v < lo ? lo : v > hi ? hi : v);
export const smooth = (x: number) => { const c = clamp(x, 0, 1); return c * c * (3 - 2 * c); };
const frac = (x: number) => x - Math.floor(x);

// ---------------------------------------------------------------------------------------------
// Seeded RNG (per character): the office must not move in unison, but must be deterministic per id.
// ---------------------------------------------------------------------------------------------

/** FNV-1a string hash -> uint32. */
export function hashSeed(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
  return h >>> 0;
}
/** mulberry32: small, fast, good-enough PRNG. Returns a function producing floats in [0, 1). */
export function createRng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------------------------------------
// One-shot gestures layered over the base pose
// ---------------------------------------------------------------------------------------------

export type GestureKind =
  | "stretch" | "lookAround" | "sip" | "phone" | "adjust" | "neckRoll" | "scratch" | "shift" // idle fidgets
  | "wave" | "nod" | "headShake" | "lookAt" | "point"; // event reactions

export const GESTURE_DURATION: Record<GestureKind, number> = {
  stretch: 3.2, lookAround: 3.4, sip: 3.6, phone: 4.2, adjust: 1.6, neckRoll: 2.2, scratch: 2.6, shift: 2.4,
  wave: 2.6, nod: 1.3, headShake: 1.4, lookAt: 2.8, point: 2.8,
};
/** Gestures that react to an event: they pre-empt idle fidgets. */
export const EVENT_GESTURES: ReadonlySet<GestureKind> = new Set<GestureKind>(["wave", "nod", "headShake", "lookAt", "point"]);

/** 0 -> 1 -> hold -> 0 envelope over u in [0,1]; exactly 0 at both ends. */
export function envelope(u: number, edge = 0.25): number {
  if (u <= 0 || u >= 1) return 0;
  return Math.min(smooth(u / edge), smooth((1 - u) / edge));
}

/** Blend a pose channel toward `v` with weight `w`. */
function to(p: Pose, k: PoseKey, v: number, w: number) { p[k] += (v - p[k]) * w; }

/** Which hand prop a gesture shows: 0 none, 1 coffee mug, 2 phone. */
export function propFor(kind: GestureKind | null, u: number): 0 | 1 | 2 {
  if (!kind || u < 0.12 || u > 0.88) return 0;
  return kind === "sip" ? 1 : kind === "phone" ? 2 : 0;
}

/**
 * Adds gesture `kind` at progress `u` (0..1) on top of the base pose `p`, in place.
 * `t` is the character clock (for fast oscillations) and `aim` the yaw of the target relative to the body
 * (used by lookAt/point). Every gesture is exactly neutral at u = 0 and u = 1.
 */
export function applyGesture(p: Pose, kind: GestureKind, u: number, t: number, aim = 0): void {
  const w = envelope(u);
  if (w <= 0) return;
  switch (kind) {
    case "stretch": {
      to(p, "lShX", -2.9, w); to(p, "rShX", -2.9, w); to(p, "lShZ", 0.35, w); to(p, "rShZ", 0.35, w);
      to(p, "lElX", -0.2, w); to(p, "rElX", -0.2, w);
      p.torsoX += (-0.16 - p.torsoX) * w; p.headX += (-0.32 - p.headX) * w;
      p.torsoZ += Math.sin(t * 9) * 0.012 * w;
      break;
    }
    case "lookAround": {
      p.headY += Math.sin(u * TAU * 1.0) * 0.85 * w + Math.sin(u * TAU * 2) * 0.1 * w;
      p.torsoY += Math.sin(u * TAU) * 0.18 * w;
      p.headX += -0.04 * w;
      break;
    }
    case "sip": {
      to(p, "rShX", -0.95, w); to(p, "rElX", -1.95, w); to(p, "rShZ", 0.08, w);
      p.headX += -0.14 * smooth((u - 0.3) / 0.15) * (1 - smooth((u - 0.65) / 0.15));
      break;
    }
    case "phone": {
      to(p, "rShX", -0.85, w); to(p, "rElX", -1.75, w); to(p, "rShZ", 0.1, w);
      to(p, "lShX", -0.7, w * 0.6); to(p, "lElX", -1.2, w * 0.6);
      p.headX += (0.42 - p.headX) * w; p.torsoX += 0.05 * w;
      p.rElX += Math.sin(t * 6) * 0.04 * w; // thumb scrolling
      break;
    }
    case "adjust": {
      p.pelvisY += Math.sin(u * TAU * 2) * 0.022 * w;
      p.torsoZ += Math.sin(u * TAU * 2) * 0.09 * w;
      p.torsoY += Math.sin(u * TAU) * 0.08 * w;
      break;
    }
    case "neckRoll": {
      p.headZ += Math.sin(u * TAU) * 0.26 * w;
      p.headX += (1 - Math.cos(u * TAU)) * 0.07 * w;
      break;
    }
    case "scratch": {
      to(p, "rShX", -2.2, w); to(p, "rElX", -1.55, w); to(p, "rShZ", 0.14, w);
      p.rElX += Math.sin(t * 14) * 0.16 * w;
      p.headZ += 0.1 * w;
      break;
    }
    case "shift": {
      const s = Math.sin(u * Math.PI);
      p.torsoZ += s * 0.08 * w; p.pelvisY -= s * 0.012 * w; p.lHipX += s * 0.06 * w; p.headZ += s * 0.04 * w;
      break;
    }
    case "wave": {
      to(p, "rShX", -2.75, w); to(p, "rShZ", 0.22, w);
      to(p, "rElX", -0.25 + Math.sin(t * 11) * 0.4, w);
      p.headX += -0.06 * w; p.headY += aim * 0.35 * w;
      break;
    }
    case "nod": {
      p.headX += (1 - Math.cos(u * TAU * 2)) * 0.5 * 0.2 * w;
      p.torsoX += (1 - Math.cos(u * TAU * 2)) * 0.5 * 0.03 * w;
      break;
    }
    case "headShake": {
      p.headY += Math.sin(u * TAU * 3) * 0.38 * w;
      p.headX += 0.1 * w;
      break;
    }
    case "lookAt": {
      p.headY += clamp(aim, -1.1, 1.1) * 0.85 * w;
      p.torsoY += clamp(aim, -1.1, 1.1) * 0.28 * w;
      p.headX += -0.04 * w;
      p.headX += (1 - Math.cos(smooth((u - 0.55) / 0.3) * TAU)) * 0.1 * w; // acknowledging nod at the end
      break;
    }
    case "point": {
      const a = clamp(aim, -1.1, 1.1);
      p.headY += a * 0.85 * w; p.torsoY += a * 0.28 * w;
      to(p, "rShX", -1.45, w); to(p, "rElX", -0.2, w); to(p, "rShZ", clamp(0.05 + a * -0.25, -0.2, 0.35), w);
      p.rElX += Math.sin(t * 7) * 0.05 * w;
      break;
    }
  }
}

const STAND_FIDGETS: GestureKind[] = ["lookAround", "stretch", "phone", "sip", "scratch", "shift", "lookAround", "shift"];
const SEAT_RELAXED_FIDGETS: GestureKind[] = ["stretch", "sip", "lookAround", "adjust", "neckRoll", "phone", "adjust", "sip"];
const SEAT_BUSY_FIDGETS: GestureKind[] = ["sip", "neckRoll", "lookAround", "adjust", "stretch", "neckRoll"];

/** Idle fidget for a placement/state, or null when the state must stay readable (blocked, error, approval...). */
export function pickFidget(mode: string, r: number): GestureKind | null {
  let list: GestureKind[] | null = null;
  switch (mode) {
    case "stand:idle": list = STAND_FIDGETS; break;
    case "seat:idle": case "seat:waiting": list = SEAT_RELAXED_FIDGETS; break;
    case "seat:working": case "seat:thinking": case "seat:reviewing": list = SEAT_BUSY_FIDGETS; break;
    default: return null;
  }
  return list[Math.min(list.length - 1, Math.floor(r * list.length))];
}
/** Seconds until the next fidget attempt (busy workers fidget less often). */
export function fidgetDelay(mode: string, r: number): number {
  const busy = mode === "seat:working" || mode === "seat:thinking" || mode === "seat:reviewing";
  return busy ? 9 + r * 14 : 4 + r * 8;
}
/** Seconds until the next blink: slower while focused, with a short double-blink chance handled by the caller. */
export function blinkDelay(mode: string, r: number): number {
  const focused = mode === "seat:working" || mode === "seat:reviewing";
  return (focused ? 3 : 2) + r * (focused ? 5 : 4);
}

// ---------------------------------------------------------------------------------------------
// Base poses
// ---------------------------------------------------------------------------------------------

/**
 * Fills `p` with the target pose of a (placement, state) mode. `t` is the character clock, `ph` a per-character
 * phase (0..TAU) and `rate` a per-character tempo (~0.85..1.15) so nobody animates in lockstep.
 * `stride` (walk phase, advances with distance) and `amp` (0..1 speed fraction) only matter for the walk.
 */
export function computePose(p: Pose, mode: string, t: number, ph = 0, rate = 1, stride = 0, amp = 1): Pose {
  for (const k of KEYS) p[k] = 0;
  const sit = mode.startsWith("seat");
  p.pelvisY = sit ? SEAT_Y : STAND_Y;
  const breathe = Math.sin(t * 1.7 * rate + ph) * 0.012;
  const sway = Math.sin(t * 0.37 * rate + ph * 1.7);
  if (sit) {
    p.lHipX = -Math.PI / 2 + 0.05; p.rHipX = -Math.PI / 2 + 0.05;
    p.lKneeX = Math.PI / 2 - 0.05; p.rKneeX = Math.PI / 2 - 0.05;
    p.pelvisY += breathe;
    p.torsoZ = sway * 0.012; // seated weight shift
  }
  const rest = () => {
    p.torsoX = 0.14; p.headX = 0.12;
    p.lShX = -0.55; p.lElX = -0.95; p.lShZ = 0.12;
    p.rShX = -0.55; p.rElX = -0.95; p.rShZ = 0.12;
  };
  if (mode === "walk") {
    const s = Math.sin(stride);
    const a = 0.25 + 0.75 * amp;
    p.pelvisY = STAND_Y + Math.abs(s) * 0.06 * a;
    p.lHipX = s * 0.75 * a; p.rHipX = -s * 0.75 * a;
    p.lKneeX = Math.max(0, Math.sin(stride + 1.3)) * 0.95 * a; p.rKneeX = Math.max(0, -Math.sin(stride + 1.3)) * 0.95 * a;
    p.lShX = -s * 0.6 * a; p.rShX = s * 0.6 * a; p.lElX = -0.2 - 0.25 * a; p.rElX = -0.2 - 0.25 * a;
    p.lShZ = 0.08; p.rShZ = 0.08;
    p.torsoX = 0.03 + 0.06 * amp; p.torsoZ = s * 0.03 * a; p.torsoY = -s * 0.1 * a;
    p.headY = s * 0.04 * a; p.headX = -0.03 - 0.02 * amp;
    return p;
  }
  switch (mode) {
    case "seat:working": {
      rest();
      // typing comes in bursts with short pauses (reading the screen, reaching for the mouse)
      const act = smooth(0.5 + 1.1 * (Math.sin(t * 0.9 * rate + ph) + 0.6 * Math.sin(t * 0.37 * rate + ph * 2.3)));
      const alt = 0.5 + 0.5 * Math.sin(t * 2.3 * rate + ph); // which hand is busier
      const f = t * 15 * rate;
      p.lElX += Math.sin(f) * 0.11 * act * (0.4 + 0.6 * alt);
      p.rElX += Math.sin(f + Math.PI) * 0.11 * act * (0.4 + 0.6 * (1 - alt));
      p.lShX += Math.sin(t * 7.3 + ph) * 0.04 * act; p.rShX += Math.sin(t * 6.1 + ph) * 0.04 * act;
      p.headX = 0.2 - 0.14 * (1 - act) + Math.sin(t * 2.2 * rate) * 0.03;
      p.headY = Math.sin(t * 0.7 * rate + ph) * 0.08 + (1 - act) * Math.sin(t * 1.1 + ph) * 0.1;
      p.torsoX += 0.03 * act;
      // during a pause the right hand drifts toward the mouse
      p.rShX += -0.12 * (1 - act); p.rShZ += -0.06 * (1 - act);
      break;
    }
    case "seat:thinking": {
      rest();
      // alternates hand-on-chin with a head scratch
      const cyc = frac(t * rate / 11 + ph / TAU);
      const sc = envelope(clamp((cyc - 0.55) / 0.3, 0, 1), 0.3);
      p.rShX = -0.95 + (-1.35 - -0.95) * sc; p.rElX = -1.9 + (0.4) * sc + Math.sin(t * 14) * 0.14 * sc; p.rShZ = 0.1;
      p.headX = -0.05 - 0.06 * sc; p.headZ = 0.12 + 0.04 * sc; p.headY = Math.sin(t * 0.8 * rate + ph) * 0.22 + 0.1;
      p.torsoX = 0.08;
      // left hand taps the desk now and then
      p.lElX += Math.max(0, Math.sin(t * 8 + ph)) * 0.1 * (0.5 + 0.5 * Math.sin(t * 0.6 + ph));
      break;
    }
    case "seat:idle": {
      rest();
      p.headX = 0.08; p.headY = Math.sin(t * 0.6 * rate + ph) * 0.15;
      p.torsoX += sway * 0.02;
      break;
    }
    case "seat:waiting": {
      rest();
      p.rElX += Math.max(0, Math.sin(t * 3 * rate + ph)) * 0.15;
      p.headX = 0.05; p.headY = Math.sin(t * 0.6 * rate + ph) * 0.35;
      p.lElX += Math.max(0, Math.sin(t * 2.2 + ph * 2)) * 0.08; // fingers drumming
      break;
    }
    case "seat:reviewing": {
      // leans into the document, scanning lines, turning a page now and then
      const lean = 0.2 + 0.07 * Math.sin(t * 0.5 * rate + ph);
      p.torsoX = lean;
      p.lShX = -1.0; p.lElX = -0.8; p.lShZ = -0.05;
      p.rShX = -1.0; p.rElX = -0.8; p.rShZ = -0.05;
      p.headX = 0.14; p.headY = Math.sin(t * 1.8 * rate + ph) * 0.24;
      const pg = envelope(clamp((frac(t * rate / 7 + ph / TAU) - 0.8) / 0.2, 0, 1), 0.3);
      p.lShX += -0.18 * pg; p.lShZ += 0.18 * pg; p.lElX += 0.25 * pg;
      p.headX += Math.sin(t * 0.9 + ph) * 0.02;
      break;
    }
    case "seat:blocked": {
      rest();
      // slumped, rubbing face; an occasional slow head shake and a deep sigh
      const sh = envelope(clamp((frac(t * rate / 7 + ph / TAU) - 0.6) / 0.35, 0, 1), 0.2);
      p.torsoX = 0.3 + Math.sin(t * 0.55 * rate + ph) * 0.03; p.headX = 0.5; p.headZ = -0.1;
      p.lShX = -1.4; p.lElX = -1.9; p.lShZ = 0.2;
      p.headY = Math.sin(t * 0.5 + ph) * 0.08 + Math.sin(t * 7) * 0.22 * sh;
      p.rElX += Math.sin(t * 0.8 + ph) * 0.05;
      break;
    }
    case "seat:awaiting_approval": {
      rest();
      // raised hand alternates with fingers tapping and a glance back over the shoulder
      const cyc = frac(t * rate / 9 + ph / TAU);
      const up = 1 - smooth((cyc - 0.55) / 0.08) * (1 - smooth((cyc - 0.92) / 0.08)); // 1 raised, 0 tapping
      p.torsoX = 0.02; p.headX = -0.02;
      p.headY = 0.5 + Math.sin(t * 1.4 * rate) * 0.08 + (1 - up) * 0.45;
      p.rShX = -0.55 + (-2.85 - -0.55) * up; p.rShZ = 0.12;
      p.rElX = -0.95 + (-0.15 - -0.95) * up + Math.sin(t * 5 * rate) * 0.28 * up;
      p.lElX += Math.max(0, Math.sin(t * 9 + ph)) * 0.14 * (1 - up);
      p.rElX += Math.max(0, Math.sin(t * 10 + ph)) * 0.1 * (1 - up);
      break;
    }
    case "stand:talking": {
      // beat gestures with phases of calm and emphasis, nods, and a lean toward the listener
      const em = 0.55 + 0.45 * Math.sin(t * 0.8 * rate + ph);
      const nod = Math.pow(Math.max(0, Math.sin(t * 2.1 * rate + ph)), 6);
      p.torsoX = 0.03 + 0.03 * em; p.torsoZ = sway * 0.03;
      p.headX = Math.sin(t * 5.3) * 0.05 * em + nod * 0.14; p.headY = Math.sin(t * 1.3 * rate + ph) * 0.12;
      p.pelvisY = STAND_Y + breathe;
      p.rShX = -0.95 + Math.sin(t * 3.1 * rate) * 0.35 * em; p.rElX = -0.85 + Math.sin(t * 4.3 * rate) * 0.45 * em; p.rShZ = 0.15;
      p.lShX = -0.65 + Math.sin(t * 2.7 * rate + 1) * 0.3 * em; p.lElX = -0.75 + Math.sin(t * 3.7 * rate) * 0.35 * em; p.lShZ = 0.15;
      break;
    }
    case "stand:completed": {
      // cheer (arm pumps + hops) -> applause -> settles into a satisfied fist pump
      const cyc = frac(t * rate / 5.5 + ph / TAU);
      const clap = smooth((cyc - 0.5) / 0.08) * (1 - smooth((cyc - 0.92) / 0.08));
      const hop = Math.abs(Math.sin(t * 5.5 * rate)) * 0.085 * (1 - clap);
      p.pelvisY = STAND_Y + hop;
      p.torsoX = -0.05; p.headX = -0.1 + clap * 0.12;
      p.lHipX = -hop * 1.2; p.rHipX = -hop * 1.2; p.lKneeX = hop * 2; p.rKneeX = hop * 2;
      const pump = Math.sin(t * 6 * rate);
      // cheer: both arms up, alternating pump
      const cLx = -2.55 + pump * 0.25, cRx = -2.55 - pump * 0.25;
      const cLe = -0.5 + pump * 0.25, cRe = -0.5 - pump * 0.25;
      // clap: hands together in front of the chest
      const cl = Math.sin(t * 13 * rate) * 0.1;
      p.lShX = cLx + (-1.2 - cLx) * clap; p.rShX = cRx + (-1.2 - cRx) * clap;
      p.lElX = cLe + (-1.55 - cLe) * clap; p.rElX = cRe + (-1.55 - cRe) * clap;
      p.lShZ = 0.12 + (-0.12 + cl - 0.12) * clap; p.rShZ = 0.12 + (-0.12 - cl - 0.12) * clap;
      break;
    }
    case "stand:error": {
      // panics (hands to the head, shaking), then slumps and shakes the head slowly
      const cyc = frac(t * rate / 7 + ph / TAU);
      const slump = smooth((cyc - 0.35) / 0.12) * (1 - smooth((cyc - 0.9) / 0.1));
      p.pelvisY = STAND_Y + breathe - slump * 0.03;
      p.torsoX = 0.12 + slump * 0.2; p.headX = 0.2 + slump * 0.3;
      p.headY = Math.sin(t * 9) * 0.18 * (1 - slump) + Math.sin(t * 2.4 + ph) * 0.3 * slump;
      const fl = Math.sin(t * 12) * 0.12 * (1 - slump);
      p.lShX = -2.3 + (-0.05 + 2.3) * slump + fl; p.rShX = -2.3 + (-0.05 + 2.3) * slump - fl;
      p.lShZ = 0.6 - 0.45 * slump; p.rShZ = 0.6 - 0.45 * slump;
      p.lElX = -1.7 + 1.45 * slump; p.rElX = -1.7 + 1.45 * slump;
      break;
    }
    default: {
      // stand:idle - breathes, shifts weight and looks around
      p.pelvisY = STAND_Y + breathe;
      p.torsoX = 0.02; p.torsoZ = sway * 0.03; p.torsoY = Math.sin(t * 0.29 * rate + ph) * 0.05;
      p.headY = Math.sin(t * 0.55 * rate + ph) * 0.6 * (Math.sin(t * 0.21 * rate + ph) > 0 ? 1 : 0.35);
      p.headX = Math.sin(t * 0.33 * rate + ph) * 0.05;
      p.lShX = 0.04 + Math.sin(t * 1.7 * rate + ph) * 0.02; p.rShX = p.lShX; p.lShZ = 0.1; p.rShZ = 0.1;
      p.lElX = -0.12; p.rElX = -0.12;
    }
  }
  return p;
}
