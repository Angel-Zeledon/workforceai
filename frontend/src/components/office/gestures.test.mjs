// Unit tests of the pure gesture/pose math (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  GESTURE_DURATION, KEYS, applyGesture, computePose, createRng, envelope, fidgetDelay, hashSeed, pickFidget, propFor, zeroPose,
} from "./gestures.ts";

const MODES = [
  "walk", "seat:working", "seat:thinking", "seat:idle", "seat:waiting", "seat:reviewing", "seat:blocked", "seat:awaiting_approval",
  "stand:talking", "stand:completed", "stand:error", "stand:idle",
];

test("seeded rng is deterministic per id and differs between ids", () => {
  const a = createRng(hashSeed("agent-1")), b = createRng(hashSeed("agent-1")), c = createRng(hashSeed("agent-2"));
  const sa = [a(), a(), a()], sb = [b(), b(), b()], sc = [c(), c(), c()];
  assert.deepEqual(sa, sb);
  assert.notDeepEqual(sa, sc);
  for (const v of sa) assert.ok(v >= 0 && v < 1);
});

test("envelope is zero at the ends and 1 in the middle", () => {
  assert.equal(envelope(0), 0);
  assert.equal(envelope(1), 0);
  assert.equal(envelope(0.5), 1);
  assert.ok(envelope(0.1) > 0 && envelope(0.1) < 1);
});

test("every gesture is neutral at u=0 and u=1", () => {
  for (const kind of Object.keys(GESTURE_DURATION)) {
    for (const u of [0, 1]) {
      const base = computePose(zeroPose(), "seat:idle", 3.3, 1, 1);
      const p = { ...base };
      applyGesture(p, kind, u, 3.3, 0.7);
      for (const k of KEYS) assert.ok(Math.abs(p[k] - base[k]) < 1e-9, `${kind}@${u} changed ${k}`);
    }
  }
});

test("gestures change the pose mid-way and stay finite and bounded", () => {
  for (const kind of Object.keys(GESTURE_DURATION)) {
    const base = computePose(zeroPose(), "stand:idle", 1.1, 0.4, 1);
    const p = { ...base };
    applyGesture(p, kind, 0.5, 1.1, 0.7);
    assert.ok(KEYS.some((k) => Math.abs(p[k] - base[k]) > 1e-3), `${kind} did nothing`);
    for (const k of KEYS) assert.ok(Number.isFinite(p[k]) && Math.abs(p[k]) < 8, `${kind} ${k}=${p[k]}`);
  }
});

test("all modes produce finite poses for many clock values and reuse the output object", () => {
  const out = zeroPose();
  for (const mode of MODES) {
    for (let t = 0; t < 120; t += 0.37) {
      const r = computePose(out, mode, t, 2.2, 1.1, t * 3, 0.8);
      assert.equal(r, out);
      for (const k of KEYS) assert.ok(Number.isFinite(out[k]) && Math.abs(out[k]) < 8, `${mode} ${k}`);
    }
  }
});

test("static pose (t=0, reduce motion) is stable", () => {
  for (const mode of MODES) {
    const a = computePose(zeroPose(), mode, 0, 0, 1), b = computePose(zeroPose(), mode, 0, 0, 1);
    assert.deepEqual(a, b);
  }
});

test("fidgets: none for states that must stay readable; relaxed states get them", () => {
  for (const mode of ["seat:blocked", "seat:awaiting_approval", "stand:error", "stand:completed", "stand:talking", "walk"]) {
    assert.equal(pickFidget(mode, 0.5), null, mode);
  }
  for (const mode of ["stand:idle", "seat:idle", "seat:waiting", "seat:working"]) {
    for (let r = 0; r < 1; r += 0.05) assert.ok(pickFidget(mode, r), `${mode} ${r}`);
  }
  assert.ok(fidgetDelay("seat:working", 0) > fidgetDelay("stand:idle", 0));
});

test("props only show mid-gesture for sip and phone", () => {
  assert.equal(propFor("sip", 0.5), 1);
  assert.equal(propFor("phone", 0.5), 2);
  assert.equal(propFor("sip", 0.05), 0);
  assert.equal(propFor("wave", 0.5), 0);
  assert.equal(propFor(null, 0.5), 0);
});
