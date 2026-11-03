// Unit tests of the folder access helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { countByLevel, defaultLevelOf, displayPath, nextLevel, sortEntries, validFolder } from "./folderAccessLogic.ts";

test("defaults: desk and projects edit, shared read, the rest none", () => {
  assert.equal(defaultLevelOf("/Agents/sales/notas", "sales"), "edit");
  assert.equal(defaultLevelOf("/Agents/legal", "sales"), "none");
  assert.equal(defaultLevelOf("/Projects/Plan/x", "sales", ["/Projects/Plan"]), "edit");
  assert.equal(defaultLevelOf("/Projects/Planes", "sales", ["/Projects/Plan"]), "none");
  assert.equal(defaultLevelOf("/Shared/Facturas", "sales"), "read");
  assert.equal(defaultLevelOf("/Clients/Acme", "sales"), "none");
});

test("choosing the default level of a manual rule removes the rule", () => {
  assert.equal(nextLevel({ level: "none", source: "manual" }, "read", "read"), "default");
  assert.equal(nextLevel({ level: "read", source: "default" }, "edit", "read"), "edit");
  assert.equal(nextLevel({ level: "read", source: "manual" }, "edit", "none"), "edit");
});

test("a folder must be absolute, below / and not the agent's own desk", () => {
  assert.ok(validFolder("/Shared/Facturas", "sales"));
  for (const p of ["", "/", "Shared", "/Shared/../Agents", "/Agents/sales", "/Agents/sales/x", "//x"]) assert.equal(validFolder(p, "sales"), false, p);
  assert.ok(validFolder("/Agents/salesman", "sales"));
});

test("entries: the fixed desk first, then by path; counts per level", () => {
  const s = sortEntries([{ path: "/Shared", level: "read", source: "default" }, { path: "/Clients/A", level: "edit", source: "manual" }, { path: "/Agents/x", level: "edit", source: "default", fixed: true }]);
  assert.deepEqual(s.map((e) => e.path), ["/Agents/x", "/Clients/A", "/Shared"]);
  assert.deepEqual(countByLevel([{ level: "edit" }, { level: "read" }, { level: "read" }]), { none: 0, read: 2, edit: 1 });
});

test("display path uses the visible root names", () => {
  assert.equal(displayPath("/Shared/Facturas", { Shared: "Compartido" }), "/Compartido/Facturas");
  assert.equal(displayPath("/", {}), "/");
});
