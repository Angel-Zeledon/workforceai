// Unit tests of the pure maths of the "Plano" office style (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { facing, floorLook, hexToNum, humanSkin, iso, mapPose, shade, turn } from "./isoMath.ts";

test("iso: greater x + z is lower on screen; x grows right, z grows left", () => {
  const a = iso(0, 0), b = iso(1, 1), c = iso(1, 0), d = iso(0, 1);
  assert.ok(b.y > a.y);
  assert.ok(c.x > a.x && d.x < a.x);
  assert.ok(iso(0, 0, 1).y < a.y);
});

test("turn follows the rotation.y convention", () => {
  const [x, z] = turn(1, 0, Math.PI / 2);
  assert.ok(Math.abs(x) < 1e-9 && Math.abs(z + 1) < 1e-9);
});

test("colours: hex parsing and shading", () => {
  assert.equal(hexToNum("#fff"), 0xffffff);
  assert.equal(hexToNum("#0a66c2"), 0x0a66c2);
  assert.equal(hexToNum("nope", 5), 5);
  assert.equal(shade(0x808080, 0.5), 0x404040);
});

test("floor: every layout kind has a palette, unknown falls back to concrete", () => {
  for (const k of ["concrete", "wood", "parquet", "carpet", "tile", "marble", "other"]) assert.ok(floorLook(k, "#ffffff").base > 0);
  assert.equal(floorLook("other", "#ffffff").base, floorLook("concrete", "#ffffff").base);
});

test("mapPose: a seated pose bends hips and knees, a standing one stays straight", () => {
  const base = { pelvisY: 0.4, torsoX: 0.1, headX: 0, lShX: -0.5, rShX: -0.5, lElX: -1, rElX: -1, lHipX: 0, rHipX: 0, lKneeX: 0, rKneeX: 0 };
  const stand = mapPose(base);
  assert.equal(stand.seated, false);
  assert.equal(stand.hipY, 30);
  const sit = mapPose({ ...base, pelvisY: 0.46, lHipX: -1.52, rHipX: -1.52, lKneeX: 1.52, rKneeX: 1.52 });
  assert.equal(sit.seated, true);
  assert.ok(sit.hipY < stand.hipY);
  assert.ok(sit.legA[0] > 1 && sit.knee[0] < -1);
  assert.ok(stand.armA[0] > 0); // arms forward
});

test("facing: walking away from the camera shows the back", () => {
  assert.equal(facing(Math.PI).back, true); // toward -z
  assert.equal(facing(0).back, false); // toward the camera
});

test("humanSkin keeps real skin tones and replaces anything else", () => {
  assert.equal(humanSkin("#c98f68"), 0xc98f68);
  assert.equal(humanSkin("#a8734f"), 0xa8734f);
  assert.notEqual(humanSkin("#3333ff"), 0x3333ff);
  assert.notEqual(humanSkin("#00ff00"), 0x00ff00);
});
