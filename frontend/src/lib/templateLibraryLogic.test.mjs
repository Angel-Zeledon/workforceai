// Unit tests of the single template library (quick requests + projects) (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { hasActiveFilters, kindFromParam, libraryCategories, libraryEmptyState, matchesSearch, showQuickBuiltinNote, showsKind } from "./templateLibraryLogic.ts";

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

test("the quick requests say they come with the app when 'Mías y de la organización' hides them", () => {
  assert.equal(showQuickBuiltinNote("org", "all", 4), true);
  assert.equal(showQuickBuiltinNote("org", "quick", 4), true);
  assert.equal(showQuickBuiltinNote("org", "project", 4), false); // kind "Proyecto": quick requests are not wanted
  assert.equal(showQuickBuiltinNote("all", "all", 4), false);
  assert.equal(showQuickBuiltinNote("builtin", "quick", 4), false);
  assert.equal(showQuickBuiltinNote("org", "all", 0), false); // the search or the category would hide them anyway
});

test("active filters: search, category, source or kind", () => {
  const none = { query: "  ", category: "all", source: "all", kind: "all" };
  assert.equal(hasActiveFilters(none), false);
  assert.equal(hasActiveFilters({ ...none, query: "cierre" }), true);
  assert.equal(hasActiveFilters({ ...none, category: "finance" }), true);
  assert.equal(hasActiveFilters({ ...none, source: "org" }), true);
  assert.equal(hasActiveFilters({ ...none, kind: "project" }), true);
});

test("the empty state never repeats or contradicts the quick-request note", () => {
  const base = { nothing: true, quickNote: false, source: "all", kind: "all" };
  assert.equal(libraryEmptyState({ ...base, nothing: false }), null);
  assert.equal(libraryEmptyState({ ...base, quickNote: true, source: "org", kind: "quick" }), null); // the note says it all
  assert.equal(libraryEmptyState({ ...base, quickNote: true, source: "org" }), "projects"); // note for quick, this for projects
  assert.equal(libraryEmptyState({ ...base, source: "org", kind: "project" }), "projects");
  assert.equal(libraryEmptyState(base), "generic");
  assert.equal(libraryEmptyState({ ...base, source: "builtin", kind: "quick" }), "generic");
});
