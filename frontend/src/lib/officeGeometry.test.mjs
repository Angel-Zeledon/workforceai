import test from "node:test";
import assert from "node:assert/strict";
import { overlap, snap, snapRot, keepInside, insideFloor, deskBox, wallSnap, corners, FLOOR } from "./officeGeometry.ts";

test("snap rounds to the grid and quarter turns", () => {
  assert.equal(snap(1.13), 1.25);
  assert.equal(snap(-0.1), -0);
  assert.equal(snapRot(1.4), Math.PI / 2);
  assert.equal(snapRot(3.0), Math.PI);
});

test("overlap: separate, touching and crossing rectangles", () => {
  const a = { x: 0, z: 0, w: 2, d: 1, rot: 0 };
  assert.equal(overlap(a, { x: 3, z: 0, w: 2, d: 1, rot: 0 }), false);
  assert.equal(overlap(a, { x: 2, z: 0, w: 2, d: 1, rot: 0 }), false); // edge to edge
  assert.equal(overlap(a, { x: 1, z: 0.2, w: 2, d: 1, rot: 0 }), true);
});

test("overlap honors rotation", () => {
  const a = { x: 0, z: 0, w: 4, d: 0.5, rot: 0 };
  const rotated = { x: 1.8, z: 0, w: 4, d: 0.5, rot: Math.PI / 2 }; // a vertical bar standing at the end of a
  assert.equal(overlap(a, rotated), true);
  assert.equal(overlap(a, { x: 1.8, z: 3, w: 4, d: 0.5, rot: Math.PI / 2 }), false);
});

test("keepInside pulls a box back into the floor", () => {
  const b = keepInside({ x: 20, z: -20, w: 2, d: 2, rot: 0 });
  assert.equal(insideFloor(b), true);
  assert.ok(Math.abs(b.x - (FLOOR.x1 - 1)) < 1e-9);
});

test("desk footprint and wall snapping", () => {
  const d = deskBox(-8, -4.5);
  assert.equal(d.w, 2.4);
  const s = wallSnap(0, -9, 0.4);
  assert.equal(s.wall, "n");
  assert.ok(s.z < -9.5);
  const w = wallSnap(-13, 2, 0.4);
  assert.equal(w.wall, "w");
  assert.equal(corners({ x: 0, z: 0, w: 2, d: 2, rot: 0 }).length, 4);
});
