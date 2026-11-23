// Unit tests of the adaptive render quality (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { DOWN_AFTER, IDLE_FPS, LOWEST_TIER, QUALITY_TIERS, RETRY_AFTER, UP_AFTER, WARMUP, isSoftwareRenderer, monitorStart, monitorStep, paceWait, shouldDraw, startingTier, targetFps, tierDpr } from "./officeQuality.ts";

/** Feeds `seconds` of frames at `fps` (asking for `target`). */
function run(s, fps, seconds, target = 60) {
  for (let t = 0; t < seconds; t += 1 / fps) monitorStep(s, 1 / fps, target);
  return s;
}

test("every tier gives up something and never asks for more than the one before", () => {
  const rank = { soft: 2, basic: 1, off: 0, full: 2, cheap: 1 };
  for (let i = 1; i < QUALITY_TIERS.length; i++) {
    const a = QUALITY_TIERS[i - 1], b = QUALITY_TIERS[i];
    assert.ok(b.dpr <= a.dpr && b.shadowMap <= a.shadowMap && b.shadowEvery >= a.shadowEvery && b.minCaster >= a.minCaster, String(i));
    assert.ok(rank[b.shadows] <= rank[a.shadows] && rank[b.ao] <= rank[a.ao], String(i));
    assert.ok((b.bloom ? 1 : 0) <= (a.bloom ? 1 : 0) && (b.tilt ? 1 : 0) <= (a.tilt ? 1 : 0), String(i));
    assert.notDeepEqual(a, b);
  }
  assert.equal(QUALITY_TIERS[LOWEST_TIER].shadows, "off");
  assert.equal(QUALITY_TIERS[LOWEST_TIER].ao, "off");
});

test("the pixel ratio never exceeds the screen's", () => {
  assert.equal(tierDpr(0, 1), 1);
  assert.equal(tierDpr(0, 2), 1.75);
  assert.equal(tierDpr(0, 1.25), 1.25);
  assert.equal(tierDpr(LOWEST_TIER, 2), 0.5);
  assert.equal(tierDpr(LOWEST_TIER - 1, 2), 0.75);
  assert.equal(tierDpr(99, 2), tierDpr(LOWEST_TIER, 2));
});

test("a fast computer stays at full quality", () => {
  const s = run(monitorStart(), 60, 30);
  assert.equal(s.tier, 0);
});

test("slow frames step the quality down, one tier at a time, after a warm-up", () => {
  const s = monitorStart();
  run(s, 18, WARMUP - 0.3);
  assert.equal(s.tier, 0, "warm-up is not judged");
  run(s, 18, DOWN_AFTER + 0.8);
  assert.equal(s.tier, 1);
  run(s, 18, WARMUP + DOWN_AFTER + 1);
  assert.equal(s.tier, 2);
  run(s, 18, 60);
  assert.equal(s.tier, LOWEST_TIER);
});

test("an office that reads as fluid (about 29 fps on an integrated GPU) keeps the full picture", () => {
  const s = run(monitorStart(), 29, 120);
  assert.equal(s.tier, 0);
});

test("a short hitch (a shader compiling) does not change the tier", () => {
  const s = run(monitorStart(), 60, 5);
  monitorStep(s, 0.4, 60); // one long frame
  run(s, 60, 0.5);
  monitorStep(s, 3, 60); // a hidden tab: ignored
  run(s, 60, 5);
  assert.equal(s.tier, 0);
});

test("room to spare steps back up, but a tier that just failed is not retried for a while (no flicker)", () => {
  const s = run(monitorStart(), 20, WARMUP + DOWN_AFTER + 1);
  assert.equal(s.tier, 1);
  run(s, 60, WARMUP + UP_AFTER + 1);
  assert.equal(s.tier, 1, "tier 0 failed moments ago");
  run(s, 60, RETRY_AFTER);
  assert.equal(s.tier, 0, "retried after a minute");
});

test("an idle office capped at 30 fps is judged against 30, and never steps up from it", () => {
  const s = run(monitorStart(), IDLE_FPS, 30, IDLE_FPS);
  assert.equal(s.tier, 0);
  const slow = run(monitorStart(), 10, 10, IDLE_FPS);
  assert.ok(slow.tier > 0);
  const t = slow.tier;
  run(slow, IDLE_FPS, 120, IDLE_FPS);
  assert.equal(slow.tier, t);
});

test("idle frames are spaced at IDLE_FPS, active ones every refresh", () => {
  assert.equal(shouldDraw(1 / 60, false), true);
  assert.equal(shouldDraw(1 / 60, true), false);
  assert.equal(shouldDraw(2 / 60, true), true);
  assert.equal(shouldDraw(1 / 30, true), true);
});

test("software renderers are recognised by the driver's name", () => {
  for (const n of ["ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero) (0x0000C0DE)), SwiftShader driver)", "llvmpipe (LLVM 15.0.7, 256 bits)", "Microsoft Basic Render Driver", "Mesa Software Rasterizer"]) assert.ok(isSoftwareRenderer(n), n);
  for (const n of ["ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)", "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0)", "Apple M2", ""]) assert.ok(!isSoftwareRenderer(n), n);
});

test("a machine without a GPU starts on the lowest tier and never climbs; a weak one starts on tier 2 and may", () => {
  const sw = startingTier({ renderer: "llvmpipe (LLVM 15)", cores: 8, memoryGb: 8 });
  assert.deepEqual(sw, { tier: LOWEST_TIER, floor: LOWEST_TIER });
  const s = monitorStart(sw.tier, sw.floor);
  run(s, 60, 120); // plenty of room: still no climb
  assert.equal(s.tier, LOWEST_TIER);
  assert.deepEqual(startingTier({ renderer: "ANGLE (NVIDIA)", cores: 8, memoryGb: 8 }), { tier: 0, floor: 0 });
  assert.equal(startingTier({ renderer: "ANGLE (Intel)", cores: 2 }).tier, 2);
  assert.equal(startingTier({ renderer: "ANGLE (Intel)", memoryGb: 2 }).floor, 0);
  const weak = monitorStart(2, 0);
  run(weak, 60, 60);
  assert.ok(weak.tier < 2, "a weak machine that turns out fast climbs");
});

test("two frames in a row over a second step down at once; one alone (a hidden tab) is ignored", () => {
  const s = monitorStart();
  run(s, 60, WARMUP + 0.2);
  monitorStep(s, 2.2, 60);
  assert.equal(s.tier, 0);
  run(s, 60, 0.3); // a normal frame in between clears the count
  monitorStep(s, 2.2, 60);
  assert.equal(s.tier, 0);
  monitorStep(s, 2.2, 60);
  assert.equal(s.tier, 1);
  monitorStep(s, 2.2, 60); // warm-up after a change: not judged again straight away
  assert.equal(s.tier, 1);
});

test("the tier caps the frame rate and the lowest tier is the gentlest", () => {
  assert.equal(targetFps(0, false), 60);
  assert.ok(targetFps(LOWEST_TIER, false) <= 24);
  assert.ok(targetFps(LOWEST_TIER, true) <= 12);
  assert.equal(shouldDraw(0.02, false, 0), true);
  assert.equal(shouldDraw(0.02, false, LOWEST_TIER), false);
  assert.equal(shouldDraw(0.05, false, LOWEST_TIER), true);
});

test("pacing rests as long as a slow frame took (capped), and never delays a fast one", () => {
  assert.equal(paceWait(16), 0);
  assert.equal(paceWait(60), 0);
  assert.equal(paceWait(250), 250);
  assert.equal(paceWait(9000), 1500);
});
