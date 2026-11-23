// Unit tests of the pure parts of the connected people of the "Plano" office (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { hslToNum, personPose, sceneFromClient, softenColor } from "./flatPeopleLogic.ts";
import { iso, unIso } from "./isoMath.ts";
import { colorFor } from "../../presence/logic.ts";

test("hslToNum: primaries and the form colorFor produces", () => {
  assert.equal(hslToNum("hsl(0, 100%, 50%)"), 0xff0000);
  assert.equal(hslToNum("hsl(120, 100%, 50%)"), 0x00ff00);
  assert.equal(hslToNum("hsl(240, 100%, 50%)"), 0x0000ff);
  assert.equal(hslToNum("nope"), 0x6b7a90);
  const c = hslToNum(colorFor("someone"));
  assert.ok(c >= 0 && c <= 0xffffff);
});

test("softenColor: less saturated, same grey level", () => {
  const c = softenColor(0xff0000, 0.5);
  const r = (c >> 16) & 255, g = (c >> 8) & 255;
  assert.ok(r < 255 && g > 0);
  assert.equal(softenColor(0x808080), 0x808080);
});

test("personPose: still pose breathes a little, walking pose swings opposite legs", () => {
  const a = personPose(false, 0), b = personPose(false, 1);
  assert.ok(Math.abs(a.hipY - b.hipY) < 1);
  const w = personPose(true, 0.2);
  assert.ok(Math.abs(w.legA[0] + w.legA[1]) < 1e-9);
  assert.ok(w.legA[0] !== 0 && w.armA[0] * w.legA[0] < 0); // arms swing against the legs
  assert.equal(w.seated, false);
});

test("unIso / sceneFromClient: a click on a drawn floor point gives that point back", () => {
  const p = iso(3, -2);
  const f = unIso(p.x, p.y);
  assert.ok(Math.abs(f.x - 3) < 1e-9 && Math.abs(f.z + 2) < 1e-9);
  const s = sceneFromClient(110 + p.x * 0.8, 60 + p.y * 0.8, { left: 10, top: 20 }, { x: 100, y: 40 }, 0.8);
  assert.ok(Math.abs(s.x - p.x) < 1e-9 && Math.abs(s.y - p.y) < 1e-9);
});
