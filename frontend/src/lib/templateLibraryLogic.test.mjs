// Unit tests of the single template library (quick requests + projects) (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { kindFromParam, libraryCategories, matchesSearch, showsKind } from "./templateLibraryLogic.ts";

test("?kind= opens the right filter; anything else shows both", () => {
  assert.equal(kindFromParam("quick"), "quick");
  assert.equal(kindFromParam("workflows"), "quick");
  assert.equal(kindFromParam("Project"), "project");
  assert.equal(kindFromParam(null), "all");
  assert.equal(kindFromParam("industries"), "all");
});

test("the filter shows one group or both", () => {
  assert.equal(showsKind("all", "quick"), true);
  assert.equal(showsKind("all", "project"), true);
  assert.equal(showsKind("quick", "project"), false);
  assert.equal(showsKind("project", "project"), true);
});

test("one list of categories: project order first, then the extra ones of the quick templates", () => {
  const order = ["finance", "operations", "legal", "sales", "general"];
  assert.deepEqual(libraryCategories(order, ["legal", "finance", ""], ["sales", "assistant", "finance", "management"]),
    ["finance", "legal", "sales", "general", "assistant", "management"]);
  assert.deepEqual(libraryCategories(order, [], []), []);
});

test("search ignores case and accents and needs every word", () => {
  assert.equal(matchesSearch("Lanzamiento de producto", "lanzamiento"), true);
  assert.equal(matchesSearch("Cierre de mes contable", "CIERRE mes"), true);
  assert.equal(matchesSearch("Auditoría anual", "auditoria"), true);
  assert.equal(matchesSearch("Cierre de mes", "cierre año"), false);
  assert.equal(matchesSearch("Cualquier cosa", "  "), true);
});
