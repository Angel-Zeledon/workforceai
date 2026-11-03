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
];
export const LOWEST_TIER = QUALITY_TIERS.length - 1;

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
}

export const DOWN_AFTER = 1.5;
export const UP_AFTER = 8;
export const RETRY_AFTER = 60;
export const WARMUP = 2.5;
/** Too slow: below this share of the target rate (25 of 60 fps, 12.5 of an idle 30). The office read as fluid at
 *  ~29 fps on an integrated GPU before quality tiers existed, and every step down costs picture (blur, no shadows), so a
 *  tier is only given up when the office really stutters. Fast enough to step up: at least this share of 60 fps. */
const SLOW = 0.42, FAST = 0.92;

export function monitorStart(tier = 0): MonitorState {
  return { tier, fps: 60, low: 0, high: 0, warm: WARMUP, failed: {}, t: 0 };
}

/**
 * One rendered frame: `dt` seconds since the previous one, `target` the rate asked for (60 or IDLE_FPS). Returns the
 * same object (mutated); its `tier` is the tier to draw at from now on.
 */
export function monitorStep(s: MonitorState, dt: number, target: number): MonitorState {
  if (!(dt > 0) || dt > 1) { s.t += Math.max(0, Math.min(dt || 0, 1)); return s; } // a hidden tab or a stall: not judged
  s.t += dt;
  const k = Math.min(1, dt / 0.6); // ~0.6 s smoothing
  s.fps += (1 / dt - s.fps) * k;
  if (s.warm > 0) { s.warm -= dt; s.low = 0; s.high = 0; return s; }
  if (s.fps < target * SLOW) { s.low += dt; s.high = 0; } else { s.low = 0; }
  // stepping up is only judged at full speed: an idle office capped at IDLE_FPS says nothing about 60 fps
  if (target >= 55 && s.fps >= 60 * FAST) s.high += dt; else s.high = 0;
  if (s.low >= DOWN_AFTER && s.tier < LOWEST_TIER) {
    s.failed[s.tier] = s.t;
    change(s, s.tier + 1);
  } else if (s.high >= UP_AFTER && s.tier > 0) {
    const up = s.tier - 1;
    const f = s.failed[up];
    if (f === undefined || s.t - f >= RETRY_AFTER) change(s, up);
    else s.high = 0;
  }
  return s;
}

function change(s: MonitorState, tier: number) {
  s.tier = tier; s.low = 0; s.high = 0; s.warm = WARMUP; s.fps = 60;
}

/**
 * Should a frame be drawn now? `sinceLast` seconds since the last drawn frame, `idle` the office is untouched. Idle
 * frames are spaced at IDLE_FPS (with a little slack so a 60 Hz screen lands on every second refresh).
 */
export function shouldDraw(sinceLast: number, idle: boolean): boolean {
  return !idle || sinceLast >= 1 / IDLE_FPS - 0.004;
}
