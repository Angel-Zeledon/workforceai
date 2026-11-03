// The style shader patch (themeRuntime.ts) is GLSL inside template strings: no compiler checks it until a browser runs
// it, and one error hides every styled material (floor, walls, furniture, bodies). GLSL has no hoisting, so every th*
// function must be defined before its first call. Run: npm run test:unit.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const src = readFileSync(new URL("./themeRuntime.ts", import.meta.url), "utf8");
const chunk = (name) => {
  const m = src.match(new RegExp("const " + name + " = `([\\s\\S]*?)`;"));
  assert.ok(m, `chunk ${name} not found`);
  return m[1];
};
const expand = (s) => s.replace("${NOISE}", chunk("NOISE"));

/** Names of th* functions called before their definition (or never defined). */
function useBeforeDefine(glsl) {
  const defs = new Map();
  for (const m of glsl.matchAll(/\b(?:float|vec2|vec3|vec4|void)\s+(th\w+)\s*\(/g)) if (!defs.has(m[1])) defs.set(m[1], m.index);
  const bad = [];
  for (const m of glsl.matchAll(/\b(th\w+)\s*\(/g)) {
    const isDefinition = /(?:float|vec2|vec3|vec4|void)\s+$/.test(glsl.slice(0, m.index));
    if (isDefinition) continue;
    const at = defs.get(m[1]);
    if (at === undefined || at > m.index) bad.push(m[1]);
  }
  return bad;
}

test("the checker catches a call before the definition", () => {
  assert.deepEqual(useBeforeDefine("float thA(vec3 p) { return thB(p); }\nfloat thB(vec3 p) { return 1.0; }"), ["thB"]);
  assert.deepEqual(useBeforeDefine("float thB(vec3 p) { return 1.0; }\nfloat thA(vec3 p) { return thB(p); }"), []);
});

test("every th* shader function is defined before it is used", () => {
  assert.deepEqual(useBeforeDefine(expand(chunk("VERT_HEAD")) + chunk("VERT_BODY")), [], "vertex shader");
  const frag = expand(chunk("FRAG_HEAD")) + chunk("FRAG_COLOR") + chunk("FRAG_ROUGH") + chunk("FRAG_NORMAL") + chunk("FRAG_IBL") + chunk("FRAG_INK") + chunk("FRAG_CEL");
  assert.deepEqual(useBeforeDefine(frag), [], "fragment shader");
});

test("the surface detail has no grid of cells (the old value noise drew blocky, Minecraft-like patches)", () => {
  const noise = chunk("NOISE");
  assert.match(noise, /float thSimplex\(vec3 v\)/);
  assert.doesNotMatch(noise, /thHash|thNoise/); // no per-cell random value blended across a cube lattice
  const all = src.slice(src.indexOf("const NOISE"), src.indexOf("const FRAG_CEL"));
  assert.doesNotMatch(all, /thNoise\(/);
  // relief from a true gradient in object space, not from screen-space derivatives of a height (2x2-pixel steps)
  assert.doesNotMatch(all, /dFdx\(h\)|dFdy\(h\)/);
});

test("the shader only adds detail around the catalog color and keeps the office's light", () => {
  // the albedo detail multiplies the color around 1 (mean kept); the room reflection adds no diffuse light
  assert.match(chunk("FRAG_COLOR"), /diffuseColor\.rgb \*= mix\(1\.0, thS\.x, uThTex\)/);
  assert.match(chunk("FRAG_IBL"), /iblIrradiance \*= 0\.0/);
  // the fragment stage uses normalMatrix: it must be declared there (three.js only declares it for the vertex stage)
  assert.match(chunk("FRAG_HEAD"), /uniform mat3 normalMatrix;/);
});
