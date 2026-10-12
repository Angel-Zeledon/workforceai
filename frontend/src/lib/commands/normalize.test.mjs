import { test } from "node:test";
import assert from "node:assert/strict";

// Mirrors normalizeCommand (client.ts): the API may send JSON null for empty lists.
const normalize = (c) => !c || typeof c.id !== "string" ? null : ({ ...c,
  aliases: { es: Array.isArray(c.aliases?.es) ? c.aliases.es : [], en: Array.isArray(c.aliases?.en) ? c.aliases.en : [] },
  args: Array.isArray(c.args) ? c.args : [], surfaces: Array.isArray(c.surfaces) ? c.surfaces : [] });

test("null lists become empty arrays and broken rows are dropped", () => {
  const c = normalize({ id: "clear", args: null, aliases: { es: ["limpiar"], en: null }, surfaces: null });
  assert.deepEqual(c.args, []);
  assert.deepEqual(c.aliases.en, []);
  assert.equal(c.args.some((a) => a.required), false);
  assert.equal(normalize(null), null);
  assert.equal(normalize({ args: [] }), null);
});
