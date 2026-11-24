// Run: node --test src/lib/agentSheetLogic.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { SHEET_TABS, resolveSheetTab } from "./agentSheetLogic.ts";

test("four tabs, each name and alias lands in one", () => {
  assert.deepEqual([...SHEET_TABS], ["who", "where", "skills", "perms"]);
  for (const k of SHEET_TABS) assert.equal(resolveSheetTab(k), k);
  assert.equal(resolveSheetTab("Puesto"), "where");
  assert.equal(resolveSheetTab("permissions"), "perms");
  assert.equal(resolveSheetTab("habilidades"), "skills");
  assert.equal(resolveSheetTab("nada"), "who");
  assert.equal(resolveSheetTab(null), "who");
});
