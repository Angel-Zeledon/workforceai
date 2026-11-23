// Adaptive render quality of the 3D office (docs/architecture/office-editor.md, "Styles" > "Speed on slow computers").
// Pure (no THREE / React), unit tested in officeQuality.test.mjs. FrameDriver.tsx measures the frames and applies the
// tier this module picks.

/** What one quality tier allows. Tier 0 is the full picture; every next tier gives up something that costs frames. */
export interface QualityTier {
  /** highest device pixel ratio drawn (the screen's own ratio is never exceeded) */
  dpr: number;
  /** shadow map: soft (PCF soft), basic (PCF) or off (only the blob / contact shadows under characters and furniture) */
  shadows: "soft" | "basic" | "off";
  /** shadow map size in texels */
  shadowMap: number;
  /** the shadow map is redrawn every this many frames (its ~500 draw calls are the biggest CPU cost after the scene) */
  shadowEvery: number;
  /** pieces smaller than this (bounding radius in meters: keys, cables, eyes, buttons) cast no shadow (0 = all do) */
  minCaster: number;
  /** ambient occlusion of the styles that use it: full (half-resolution, medium quality), cheap, or off */
  ao: "full" | "cheap" | "off";
  bloom: boolean;
  /** tilt-shift (bricks' miniature photo) */
  tilt: boolean;
}

export const QUALITY_TIERS: QualityTier[] = [
  { dpr: 1.75, shadows: "soft", shadowMap: 2048, shadowEvery: 1, minCaster: 0, ao: "full", bloom: true, tilt: true },
  { dpr: 1.25, shadows: "basic", shadowMap: 2048, shadowEvery: 1, minCaster: 0.06, ao: "cheap", bloom: true, tilt: false },
  { dpr: 1, shadows: "basic", shadowMap: 2048, shadowEvery: 2, minCaster: 0.15, ao: "off", bloom: false, tilt: false },
  { dpr: 0.75, shadows: "off", shadowMap: 1024, shadowEvery: 2, minCaster: 0.15, ao: "off", bloom: false, tilt: false },
  // the floor for machines that draw in software (no GPU): a small, soft picture at a gentle rate, so the tab always answers
  { dpr: 0.5, shadows: "off", shadowMap: 1024, shadowEvery: 2, minCaster: 0.3, ao: "off", bloom: false, tilt: false },
];
export const LOWEST_TIER = QUALITY_TIERS.length - 1;
/** Frame rate cap per tier while someone uses the office (the idle rate is IDLE_FPS, or half the cap if lower). */
export const TIER_FPS = [60, 60, 60, 45, 24];

/** What the GL driver calls itself when there is no GPU behind it (SwiftShader, llvmpipe, Microsoft Basic Render...). */
const SOFTWARE_RE = /swiftshader|llvmpipe|softpipe|lavapipe|software|basic render|mesa offscreen|cpu/i;
export function isSoftwareRenderer(name: string): boolean { return SOFTWARE_RE.test(name || ""); }

/**
 * Where the monitor starts, decided before the first frame: a machine without a GPU starts (and stays) on the lowest
 * tier, a weak one (2 cores or 2 GB or less) on tier 2 and may climb. Nobody should have to wait for the monitor to
 * discover a frame that takes seconds.
 */
export function startingTier(env: { renderer?: string; memoryGb?: number; cores?: number }): { tier: number; floor: number } {
  if (isSoftwareRenderer(env.renderer || "")) return { tier: LOWEST_TIER, floor: LOWEST_TIER };
  const weak = (env.memoryGb !== undefined && env.memoryGb <= 2) || (env.cores !== undefined && env.cores > 0 && env.cores <= 2);
  return { tier: weak ? 2 : 0, floor: 0 };
}

/** Frame rate asked for at a tier: 60 (capped by the tier) while in use, IDLE_FPS (at most half the cap) when idle. */
export function targetFps(tier: number, idle: boolean): number {
  const cap = TIER_FPS[Math.max(0, Math.min(LOWEST_TIER, tier))];
  return idle ? Math.min(IDLE_FPS, Math.round(cap / 2)) : cap;
}

/** Frames per second the office is drawn at while nobody touches it (characters still move, just at half rate). */
export const IDLE_FPS = 30;
/** Seconds without pointer, wheel or keyboard input before the office drops to IDLE_FPS. */
export const IDLE_AFTER = 2.5;

/** Device pixel ratio drawn at a tier on a screen of ratio `screen` (never above the screen's own). */
export function tierDpr(tier: number, screen: number): number {
  const t = QUALITY_TIERS[Math.max(0, Math.min(LOWEST_TIER, tier))];
  return Math.max(0.5, Math.min(screen || 1, t.dpr));
}

/**
 * The monitor: an exponentially smoothed frame rate compared with the rate the office is asking for (60 when someone is
 * using it, IDLE_FPS when idle). Steps down a tier after `DOWN_AFTER` seconds well below the target, back up after
 * `UP_AFTER` seconds of full speed. Hysteresis keeps it from flickering: after a tier fails, the monitor does not try it
 * again for `RETRY_AFTER` seconds, and the first `WARMUP` seconds after any change (shaders compiling, a style switch)
 * are not judged.
 */
export interface MonitorState {
  tier: number;
  /** smoothed frames per second */
  fps: number;
  /** seconds spent too slow / fast enough so far */
  low: number; high: number;
  /** seconds left before frames are judged */
  warm: number;
  /** clock (seconds) when each tier last failed (tier index -> time) */
  failed: Record<number, number>;
  /** the monitor's clock, seconds */
  t: number;
  /** the tier the monitor never climbs above (software rendering: the lowest) */
  floor: number;
  /** consecutive frames that each took more than a second */
  stalls: number;
}

export const DOWN_AFTER = 1.5;
export const UP_AFTER = 8;
export const RETRY_AFTER = 60;
export const WARMUP = 2.5;
/** Too slow: below this share of the target rate (25 of 60 fps, 12.5 of an idle 30). The office read as fluid at
 *  ~29 fps on an integrated GPU before quality tiers existed, and every step down costs picture (blur, no shadows), so a
 *  tier is only given up when the office really stutters. Fast enough to step up: at least this share of 60 fps. */
const SLOW = 0.42, FAST = 0.92;

export function monitorStart(tier = 0, floor = 0): MonitorState {
  return { tier: Math.max(tier, floor), fps: 60, low: 0, high: 0, warm: WARMUP, failed: {}, t: 0, floor, stalls: 0 };
}

/**
 * One rendered frame: `dt` seconds since the previous one, `target` the rate asked for (60 or IDLE_FPS). Returns the
 * same object (mutated); its `tier` is the tier to draw at from now on.
 */
export function monitorStep(s: MonitorState, dt: number, target: number, active = target >= 55): MonitorState {
  if (!(dt > 0)) return s;
  if (dt > 1) {
    // One frame over a second is a hidden tab or a hitch: not judged. Two in a row is a machine that cannot draw this
    // picture (the frames after a shader compile are the ones that count): step down at once, no waiting.
    s.t += 1;
    s.stalls++;
    if (s.stalls >= 2 && s.warm <= 0 && s.tier < LOWEST_TIER) { s.failed[s.tier] = s.t; change(s, s.tier + 1); }
    return s;
  }
  s.stalls = 0;
  s.t += dt;
  const k = Math.min(1, dt / 0.6); // ~0.6 s smoothing
  s.fps += (1 / dt - s.fps) * k;
  if (s.warm > 0) { s.warm -= dt; s.low = 0; s.high = 0; return s; }
  if (s.fps < target * SLOW) { s.low += dt; s.high = 0; } else { s.low = 0; }
  // stepping up is only judged at full speed: an idle office capped at IDLE_FPS says nothing about 60 fps
  if (active && s.fps >= target * FAST) s.high += dt; else s.high = 0;
  if (s.low >= DOWN_AFTER && s.tier < LOWEST_TIER) {
    s.failed[s.tier] = s.t;
    change(s, s.tier + 1);
  } else if (s.high >= UP_AFTER && s.tier > s.floor) {
    const up = s.tier - 1;
    const f = s.failed[up];
    if (f === undefined || s.t - f >= RETRY_AFTER) change(s, up);
    else s.high = 0;
  }
  return s;
}

function change(s: MonitorState, tier: number) {
  s.tier = tier; s.low = 0; s.high = 0; s.warm = WARMUP; s.fps = 60; s.stalls = 0;
}

/**
 * Should a frame be drawn now? `sinceLast` seconds since the last drawn frame, `idle` the office is untouched. Idle
 * frames are spaced at IDLE_FPS (with a little slack so a 60 Hz screen lands on every second refresh).
 */
export function shouldDraw(sinceLast: number, idle: boolean, tier = 0): boolean {
  const target = targetFps(tier, idle);
  return target >= 60 || sinceLast >= 1 / target - 0.004;
}

/** A drawn frame that holds the main thread longer than this (ms) is slow enough to be felt: the next draw waits. */
export const PACE_OVER = 100;
/**
 * Pacing for machines that cannot keep up even at the lowest tier (software GL blocks the main thread inside the draw
 * and the commit that follows): after a frame that cost `costMs` (time from its start to the next animation frame
 * callback) the office rests for as long again, so the page keeps at least half of every second for clicks, typing and
 * closing the tab. Fast frames are never delayed.
 */
export function paceWait(costMs: number): number {
  return costMs > PACE_OVER ? Math.min(costMs, 1500) : 0;
}
