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
  assert.deepEqual(useBeforeDefine(expand(chunk("FRAG_HEAD")) + chunk("FRAG_NORMAL") + chunk("FRAG_INK")), [], "fragment shader");
});
