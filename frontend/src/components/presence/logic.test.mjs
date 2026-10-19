// Unit tests of the pure presence logic (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  approachable, clampToFloor, colorFor, createMoveThrottle, dist, initials, keyDirection, nearby, nextNear, shouldPing, smoothToward, stepToward,
} from "./logic.ts";

const body = (x, z, status = "available") => ({ x, z, status });

test("nextNear has hysteresis between the enter and the exit radius", () => {
  assert.equal(nextNear(false, 2.6), false);
  assert.equal(nextNear(false, 2.4), true);
  assert.equal(nextNear(true, 3.0), true);
  assert.equal(nextNear(true, 3.3), false);
});

test("nearby lists people nearest first and remembers who was near", () => {
  const self = body(0, 0);
  const others = { b: body(2, 0), c: body(1, 0), far: body(9, 9) };
  const first = nearby(self, others, new Set());
  assert.deepEqual(first.map((n) => n.id), ["c", "b"]);
  assert.ok(first.every((n) => n.canTalk));
  // b drifts to 3.0 m: still near because it was near, but a stranger at 3.0 m is not
  const moved = { b: body(3, 0), s: body(3, 0) };
  const next = nearby(self, moved, new Set(["b"]));
  assert.deepEqual(next.map((n) => n.id), ["b"]);
});

test("nobody gets an offer while someone is in focus, meeting or away", () => {
  for (const st of ["focus", "meeting", "away"]) {
    assert.equal(approachable(st), false);
    assert.equal(nearby(body(0, 0), { b: body(1, 0, st) }, new Set())[0].canTalk, false, `other ${st}`);
    assert.equal(nearby(body(0, 0, st), { b: body(1, 0) }, new Set())[0].canTalk, false, `self ${st}`);
  }
  assert.equal(approachable("available"), true);
});

test("throttle: first goes, bursts are held, the last position is flushed", () => {
  const t = createMoveThrottle({ minIntervalMs: 100, minDelta: 0.05 });
  assert.deepEqual(t.offer({ x: 0, z: 0 }, 1000), { x: 0, z: 0 });
  assert.equal(t.offer({ x: 1, z: 0 }, 1030), null, "too soon");
  assert.equal(t.offer({ x: 2, z: 0 }, 1060), null, "still too soon");
  assert.equal(t.pending(), true);
  assert.equal(t.flush(1070), null, "interval not over");
  assert.deepEqual(t.flush(1110), { x: 2, z: 0 }, "the latest held position wins");
  assert.equal(t.pending(), false);
  assert.equal(t.flush(2000), null, "nothing left to flush");
});

test("throttle: a tiny movement is not worth a frame, enough movement is", () => {
  const t = createMoveThrottle({ minIntervalMs: 100, minDelta: 0.05 });
  t.offer({ x: 0, z: 0 }, 0);
  assert.equal(t.offer({ x: 0.01, z: 0 }, 500), null);
  assert.equal(t.pending(), false);
  assert.deepEqual(t.offer({ x: 0.5, z: 0 }, 600), { x: 0.5, z: 0 });
});

test("throttle caps a 60 fps walk at about 8 frames per second", () => {
  const t = createMoveThrottle({ minIntervalMs: 120, minDelta: 0.06 });
  let sent = 0;
  for (let i = 0; i < 600; i++) { // 10 s at 60 Hz
    const now = i * (1000 / 60);
    if (t.offer({ x: i * 0.05, z: 0 }, now)) sent++;
  }
  assert.ok(sent <= 10 * (1000 / 120) + 2, `sent ${sent}`);
  assert.ok(sent >= 60, `sent ${sent}`);
});

test("stepToward walks at a constant speed and stops on the target", () => {
  let p = { x: 0, z: 0 };
  const target = { x: 3, z: 4 }; // 5 m away
  let r = stepToward(p, target, 2, 1);
  assert.ok(Math.abs(dist(r.pos, p) - 2) < 1e-9);
  assert.equal(r.arrived, false);
  p = r.pos;
  r = stepToward(p, target, 2, 1);
  p = r.pos;
  r = stepToward(p, target, 2, 1);
  assert.equal(r.arrived, true);
  assert.deepEqual(r.pos, target);
});

test("keyDirection is normalised and ignores opposite keys", () => {
  assert.equal(keyDirection(new Set()), null);
  assert.deepEqual(keyDirection(new Set(["d"])), { x: 1, z: 0 });
  assert.equal(keyDirection(new Set(["a", "d"])), null);
  const d = keyDirection(new Set(["w", "arrowright"]));
  assert.ok(Math.abs(Math.hypot(d.x, d.z) - 1) < 1e-9);
  assert.ok(d.z < 0 && d.x > 0);
});

test("clampToFloor keeps people inside the office", () => {
  assert.deepEqual(clampToFloor({ x: 99, z: -99 }), { x: 13.9, z: -9.7 });
  assert.deepEqual(clampToFloor({ x: 1, z: 2 }), { x: 1, z: 2 });
});

test("shouldPing sends at most one activity ping per interval", () => {
  assert.equal(shouldPing(0, 10_000), false);
  assert.equal(shouldPing(0, 30_000), true);
});

test("smoothToward converges without overshooting, independent of the frame rate", () => {
  let a = { x: 0, z: 0 }, b = { x: 0, z: 0 };
  for (let i = 0; i < 60; i++) a = smoothToward(a, { x: 10, z: 0 }, 1 / 60);
  for (let i = 0; i < 30; i++) b = smoothToward(b, { x: 10, z: 0 }, 1 / 30);
  assert.ok(a.x < 10 && b.x < 10);
  assert.ok(Math.abs(a.x - b.x) < 1e-6, "same time, same place at 60 and 30 fps");
});

test("initials and colour are stable", () => {
  assert.equal(initials("Ana García Ruiz"), "AR");
  assert.equal(initials("bob"), "BO");
  assert.equal(initials("  "), "?");
  assert.equal(colorFor("u-1"), colorFor("u-1"));
  assert.notEqual(colorFor("u-1"), colorFor("u-2"));
});
