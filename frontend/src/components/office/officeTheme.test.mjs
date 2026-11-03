// Unit tests of the office style transforms (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync, statSync, readdirSync } from "node:fs";
import { APPEAR_SECONDS, PBR_KINDS, STYLE_LOOKS, appearPose, bodySquash, lookOf, pbrKindsOf, stopMotionTick, styleMaterial, stylePose } from "./officeTheme.ts";
import { OFFICE_STYLES, normalizeLayout } from "../../lib/officeLayoutModel.ts";

const MAT = { roughness: 0.8, metalness: 0, transparent: false };
const REST = { y: 0, sx: 1, sy: 1, sz: 1, rx: 0, ry: 0, visible: true };
const pose = () => ({ pelvisY: 0.4, lKneeX: 0.6, rKneeX: 0.3, lElX: -1, rElX: -1, headY: 0.4, headX: 0, headZ: 0, torsoZ: 0.1, torsoY: 0.05, torsoX: 0.1, lHipX: 0.5, rHipX: -0.5, lShX: 0.3, rShX: -0.3 });

test("six styles, each with a look", () => {
  assert.deepEqual(OFFICE_STYLES, ["classic", "bricks", "clay", "paper", "toon", "real"]);
  for (const s of OFFICE_STYLES) assert.ok(STYLE_LOOKS[s], s);
  assert.equal(lookOf(undefined), STYLE_LOOKS.classic);
});

test("classic changes nothing: materials, poses, squash and entrances", () => {
  assert.deepEqual(styleMaterial(MAT, "classic"), { roughness: 0.8, metalness: 0, flat: false, cel: false, ink: 0, lump: 0, surface: 0, bump: 0, tex: 0, pbr: null, tile: 1, roughVar: 0, envIntensity: 0 });
  // no extra cost in classic: no post-processing, no reflections, no texture download
  assert.equal(STYLE_LOOKS.classic.post, null);
  assert.equal(STYLE_LOOKS.classic.env, "none");
  assert.deepEqual(pbrKindsOf("classic"), []);
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
  // every style keeps today's palette in its picker preview
  for (const s of OFFICE_STYLES) assert.deepEqual(STYLE_LOOKS[s].swatch, STYLE_LOOKS.classic.swatch);
});

test("each style renders differently", () => {
  assert.equal(styleMaterial(MAT, "bricks").roughness < 0.3, true); // glossy molded plastic
  assert.equal(styleMaterial(MAT, "bricks").metalness, 0);
  const clay = styleMaterial(MAT, "clay");
  assert.equal(clay.surface, 1); assert.ok(clay.lump > 0); assert.ok(clay.roughness >= 0.55 && clay.roughness < 0.8); // waxy matte
  const paper = styleMaterial(MAT, "paper");
  assert.equal(paper.flat, true); assert.equal(paper.surface, 2); assert.ok(paper.ink > 0);
  assert.equal(styleMaterial(MAT, "toon").cel, true);
  assert.ok(STYLE_LOOKS.toon.outline && STYLE_LOOKS.toon.outline.px > STYLE_LOOKS.paper.outline.px); // thick ink vs thin cut edges
  // realistic picks the real-world surface from the finish
  assert.equal(styleMaterial({ roughness: 0.35, metalness: 0.6, transparent: false }, "real").surface, 4); // brushed metal
  assert.equal(styleMaterial({ roughness: 1, metalness: 0, transparent: false }, "real").surface, 5); // fabric
  assert.equal(styleMaterial({ roughness: 0.8, metalness: 0, transparent: false }, "real").surface, 3); // wood
  assert.equal(styleMaterial({ roughness: 0.08, metalness: 0, transparent: true }, "real").surface, 0); // glass stays clear
  // clay keeps glass as glass
  assert.equal(styleMaterial({ roughness: 0.08, metalness: 0, transparent: true }, "clay").lump, 0);
});

test("the floor and the walls carry the style too, so the biggest surfaces read at the default camera", () => {
  for (const s of ["clay", "paper", "real", "bricks"]) {
    for (const role of ["ground", "wall"]) {
      const o = styleMaterial(MAT, s, role);
      assert.ok(o.surface > 0 && o.bump > 0, `${s} ${role}`);
    }
  }
  assert.equal(styleMaterial(MAT, "bricks", "ground").surface, 11); // a studded baseplate
  assert.equal(styleMaterial({ ...MAT, hint: "backdrop" }, "bricks", "ground").surface, 0); // not the ground outside
  assert.equal(styleMaterial(MAT, "bricks", "wall").surface, 10); // brick courses
  assert.equal(styleMaterial(MAT, "real", "ground").surface, 8); // polished concrete
  assert.equal(styleMaterial(MAT, "clay", "ground").lump, 0); // the floor never wobbles
  assert.equal(styleMaterial(MAT, "paper", "ground").ink, 0);
});

test("style poses: toy figures keep their knees straight, paper flutters, realistic shifts its weight", () => {
  const b = stylePose(pose(), STYLE_LOOKS.bricks.motion, true, false, 1, 0, 1);
  assert.equal(b.lKneeX, 0); assert.equal(b.rKneeX, 0);
  const seated = stylePose({ ...pose(), lKneeX: 1.5 }, STYLE_LOOKS.bricks.motion, false, true, 1, 0, 0);
  assert.equal(seated.lKneeX, 1.5);
  assert.notEqual(stylePose(pose(), STYLE_LOOKS.paper.motion, false, false, 1, 0, 0).torsoZ, 0.1);
  assert.notEqual(stylePose(pose(), STYLE_LOOKS.real.motion, true, false, 1, 0, 1).torsoZ, pose().torsoZ * 0.8);
  assert.ok(stylePose(pose(), STYLE_LOOKS.toon.motion, true, false, 1, 0, 1).torsoX > 0.2); // leans into the run
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
  for (let i = 0; i < 60; i++) if (stopMotionTick(acc, 1 / 60, 12)) ticks++;
  assert.ok(ticks >= 11 && ticks <= 12, String(ticks));
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

test("the layout keeps a known style and drops unknown or default ones", () => {
  assert.equal(normalizeLayout({ style: "bricks" }).style, "bricks");
  assert.equal(normalizeLayout({ style: "classic" }).style, undefined);
  assert.equal(normalizeLayout({ style: "neon" }).style, undefined);
  assert.equal("style" in normalizeLayout({}), false);
});

test("body proportions: chubby clay figures with sausage limbs, slimmer realistic people, classic untouched", () => {
  assert.deepEqual(STYLE_LOOKS.classic.body, { head: 1, torsoW: 1, torsoH: 1, limb: 1 });
  const c = STYLE_LOOKS.clay.body, r = STYLE_LOOKS.real.body;
  assert.ok(c.head > 1 && c.torsoW > 1.1 && c.limb > 1.3);
  assert.ok(r.head < 0.9 && r.torsoH > 1.05 && r.limb < 1);
  assert.ok(STYLE_LOOKS.paper.motion.depth < 0.25); // almost flat cut-outs
  assert.ok(STYLE_LOOKS.paper.outline.shadow > 0); // lifted cut-out shadow
});

test("clay is smooth: relief only, never color stains", () => {
  for (const role of ["item", "figure", "ground", "wall"]) {
    const o = styleMaterial(MAT, "clay", role);
    assert.equal(o.tex, 0, role);
    assert.ok(o.bump > 0, role);
  }
  assert.equal(STYLE_LOOKS.clay.motion.stopFps, 12); // stop-motion on twelves
});

test("realistic uses the photographed texture sets, matched to the finish and to the floor's kind", () => {
  assert.equal(styleMaterial({ roughness: 0.8, metalness: 0, transparent: false }, "real").pbr, "wood");
  assert.equal(styleMaterial({ roughness: 1, metalness: 0, transparent: false }, "real").pbr, "fabric");
  assert.equal(styleMaterial({ roughness: 0.35, metalness: 0.6, transparent: false }, "real").pbr, "metal");
  assert.equal(styleMaterial(MAT, "real", "wall").pbr, "plaster");
  assert.equal(styleMaterial(MAT, "real", "ground").pbr, "concrete");
  assert.equal(styleMaterial({ ...MAT, hint: "parquet" }, "real", "ground").pbr, "wood");
  assert.equal(styleMaterial({ ...MAT, hint: "carpet" }, "real", "ground").pbr, "fabric");
  assert.equal(styleMaterial({ ...MAT, hint: "marble" }, "real", "ground").pbr, null);
  assert.equal(styleMaterial({ ...MAT, hint: "backdrop" }, "real", "ground").pbr, null);
  // the detail never replaces the color: it varies the shade around the catalog color by less than its own value
  for (const role of ["item", "figure", "ground", "wall"]) {
    const o = styleMaterial(MAT, "real", role);
    assert.ok(o.tex >= 0 && o.tex <= 0.8 && o.roughVar <= 0.6, role);
  }
  assert.deepEqual(pbrKindsOf("real"), PBR_KINDS);
  for (const s of ["bricks", "clay", "paper", "toon"]) assert.deepEqual(pbrKindsOf(s), [], s);
});

test("the texture sets and the HDRI ship with the app, credited, within the 4 MB budget", () => {
  const pub = new URL("../../../public/office/", import.meta.url);
  let total = 0;
  for (const k of PBR_KINDS) for (const f of [`${k}.jpg`, `${k}_r.jpg`]) total += statSync(new URL(`pbr/${f}`, pub)).size;
  total += statSync(new URL("env/room_512.hdr", pub)).size;
  assert.ok(total <= 4 * 1024 * 1024, String(total));
  const credits = readFileSync(new URL("CREDITS.md", pub), "utf8");
  for (const k of PBR_KINDS) assert.ok(credits.includes(`pbr/${k}.jpg`), k);
  assert.ok(credits.includes("CC0") && credits.includes("env/room_512.hdr"));
  // nothing else hides in the folders (every asset is listed in the credits)
  for (const f of readdirSync(new URL("pbr/", pub))) assert.ok(credits.includes(f), f);
});

test("every non-classic style has its post-processing; reflections where the finish is glossy", () => {
  for (const s of ["bricks", "clay", "paper", "toon", "real"]) assert.ok(STYLE_LOOKS[s].post, s);
  assert.equal(STYLE_LOOKS.real.env, "hdri");
  assert.equal(STYLE_LOOKS.bricks.env, "hdri");
  assert.ok(STYLE_LOOKS.bricks.post.ao > STYLE_LOOKS.real.post.ao); // deep joints between toy bricks
  assert.ok(STYLE_LOOKS.toon.outline.boil > 0 && STYLE_LOOKS.toon.outline.boilFps <= 12); // lines redrawn by hand
  assert.ok(styleMaterial(MAT, "bricks").envIntensity > 0); // glossy plastic reflects
});
