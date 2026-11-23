// Unit tests of the office style transforms (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import { APPEAR_SECONDS, STYLE_LOOKS, appearPose, bodySquash, lookOf, stopMotionTick, styleMaterial, stylePose } from "./officeTheme.ts";
import { LEGACY_STYLES, OFFICE_STYLES, SPACE_EFFECTS, STYLES_WITHOUT_EDITING, normalizeLayout, normalizeStyle } from "../../lib/officeLayoutModel.ts";

const MAT = { roughness: 0.8, metalness: 0, transparent: false };
const REST = { y: 0, sx: 1, sy: 1, sz: 1, rx: 0, ry: 0, visible: true };
const pose = () => ({ pelvisY: 0.4, lKneeX: 0.6, rKneeX: 0.3, lElX: -1, rElX: -1, headY: 0.4, headX: 0, headZ: 0, torsoZ: 0.1, torsoY: 0.05, torsoX: 0.1, lHipX: 0.5, rHipX: -0.5, lShX: 0.3, rShX: -0.3 });

test("five styles, each with a look, in the order of the menu", () => {
  assert.deepEqual(OFFICE_STYLES, ["classic", "studio", "flat", "clay", "pixel"]);
  for (const s of OFFICE_STYLES) assert.ok(STYLE_LOOKS[s], s);
  assert.equal(lookOf(undefined), STYLE_LOOKS.classic);
});

test("classic changes nothing: materials, poses, squash and entrances", () => {
  assert.deepEqual(styleMaterial(MAT, "classic"), { roughness: 0.8, metalness: 0, flat: false, cel: false, ink: 0, lump: 0, surface: 0, bump: 0, tex: 0, pbr: null, tile: 1, roughVar: 0, envIntensity: 0 });
  assert.equal(STYLE_LOOKS.classic.post, null); // no extra cost in classic: no post-processing
  assert.deepEqual(stylePose(pose(), STYLE_LOOKS.classic.motion, true, false, 3, 1, 2), pose());
  assert.deepEqual(bodySquash(STYLE_LOOKS.classic.motion, true, 1, 0, 2), [1, 1]);
  assert.deepEqual(appearPose("none", 0), REST);
  assert.equal(APPEAR_SECONDS[STYLE_LOOKS.classic.appear], 0);
});

test("styles never carry colors: the material transform has no color in or out", () => {
  for (const s of OFFICE_STYLES) for (const role of ["item", "figure", "ground", "wall"]) {
    const o = styleMaterial({ roughness: 0.5, metalness: 0.6, transparent: false }, s, role);
    assert.ok(!("color" in o) && !("emissive" in o), s);
  }
});

test("Maqueta is matte warm clay with a tilt-shift, no stop-motion and springs; Pixel is flat bands at 6 poses a second", () => {
  const clay = styleMaterial(MAT, "clay");
  assert.equal(clay.surface, 1); assert.ok(clay.lump > 0); assert.ok(clay.roughness >= 0.75); assert.equal(clay.metalness, 0);
  assert.equal(styleMaterial({ roughness: 0.08, metalness: 0, transparent: true }, "clay").lump, 0); // glass stays glass
  for (const role of ["item", "figure", "ground", "wall"]) { const o = styleMaterial(MAT, "clay", role); assert.equal(o.tex, 0, role); assert.ok(o.bump > 0, role); }
  assert.equal(styleMaterial(MAT, "clay", "ground").lump, 0); // the floor never wobbles
  const c = STYLE_LOOKS.clay;
  assert.ok(c.post && c.post.tilt > 0); assert.equal(c.warmLight, true); assert.equal(c.motion.stopFps, 0); assert.equal(c.motion.spring, true);
  assert.ok(c.body.head > 1 && c.body.limb > 1.3);
  const p = STYLE_LOOKS.pixel;
  assert.equal(p.pixel, true); assert.equal(p.cel, true); assert.equal(p.motion.stopFps, 6); assert.equal(p.post, null);
  assert.equal(styleMaterial(MAT, "pixel").cel, true);
  assert.ok(styleMaterial(MAT, "pixel").roughness >= 0.85);
  assert.equal(STYLE_LOOKS.classic.pixel, undefined);
});

test("squash and stretch keeps the volume", () => {
  for (const [w, h] of [bodySquash(STYLE_LOOKS.clay.motion, true, 1, 0, 0.3), bodySquash(STYLE_LOOKS.clay.motion, false, 2, 1, 0, "stand:completed")]) {
    assert.ok(Math.abs(w * w * h - 1) < 1e-9);
    assert.notEqual(h, 1);
  }
});

test("stop-motion advances in whole frames", () => {
  const acc = [0];
  let ticks = 0;
  for (let i = 0; i < 60; i++) if (stopMotionTick(acc, 1 / 60, 6)) ticks++;
  assert.ok(ticks >= 5 && ticks <= 6, String(ticks));
  assert.equal(stopMotionTick([0], 0.001, 0), true);
});

test("every entrance starts away from rest and ends exactly at rest", () => {
  for (const k of ["rise", "stack", "unfold", "squish", "pop"]) {
    const a = appearPose(k, 0.05);
    const moved = a.y !== 0 || a.sx !== 1 || a.sy !== 1 || a.rx !== 0 || !a.visible;
    assert.ok(moved, k);
    assert.deepEqual(appearPose(k, 1), REST, k);
  }
});

test("the layout keeps a known style, drops unknown or default ones and migrates the old names", () => {
  for (const s of ["studio", "flat", "clay", "pixel"]) assert.equal(normalizeLayout({ style: s }).style, s);
  assert.equal(normalizeLayout({ style: "classic" }).style, undefined);
  assert.equal(normalizeLayout({ style: "neon" }).style, undefined);
  assert.equal("style" in normalizeLayout({}), false);
  // saved before the five-style menu
  assert.equal(normalizeLayout({ style: "bricks" }).style, "pixel");
  assert.equal(normalizeLayout({ style: "clay" }).style, "clay");
  assert.equal(normalizeLayout({ style: "paper" }).style, "flat");
  assert.equal(normalizeLayout({ style: "real" }).style, "studio");
  assert.equal(normalizeLayout({ style: "toon" }).style, undefined); // toon -> classic
  assert.deepEqual(Object.keys(LEGACY_STYLES).sort(), ["bricks", "clay", "paper", "real", "toon"]);
  assert.equal(normalizeStyle("__proto__"), "classic");
  assert.equal(normalizeStyle(undefined), "classic");
});

test("the editor moves pieces on the normal view in the styles that cannot edit, and Space shows what each style uses", () => {
  assert.deepEqual(STYLES_WITHOUT_EDITING, ["studio", "flat"]);
  for (const s of OFFICE_STYLES) assert.ok(SPACE_EFFECTS[s].wallColor && SPACE_EFFECTS[s].floor && SPACE_EFFECTS[s].trim && SPACE_EFFECTS[s].windows, s);
  // Plano has no light of the day and no view; the 3D styles have light
  assert.equal(SPACE_EFFECTS.flat.light, false);
  assert.equal(SPACE_EFFECTS.flat.view, false);
  for (const s of ["classic", "clay", "pixel", "studio"]) assert.ok(SPACE_EFFECTS[s].light, s);
}
);
