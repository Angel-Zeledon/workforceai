// Unit tests of the department drive logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { myDepartments, pendingByPath, filterRows, proposedRows, defaultAgent, crumbs, pathKey, pendingUnder, findDepartment, deptView, lastChange } from "./deptLogic.ts";

const off = (o) => ({ id: "x", name: "x", general: false, is_member: true, folder_path: "/Offices/X", agents: [], ...o });
const ap = (id, path, over = {}) => ({ id, status: "pending", context: path === undefined ? {} : { path, action: "write" }, ...over });

test("myDepartments keeps member, non-general, with folder, in order", () => {
  const r = myDepartments([off({ id: "a" }), off({ id: "g", general: true }), off({ id: "n", is_member: false }), off({ id: "f", folder_path: "" }), off({ id: "b" })]);
  assert.deepEqual(r.map((o) => o.id), ["a", "b"]);
});

test("pendingByPath ignores no-path, resolved, outside; groups case-insensitively", () => {
  const m = pendingByPath([
    ap("1", "/Offices/Legal/A.md"), ap("2", "/offices/legal/a.md"), ap("3", undefined),
    ap("4", "/Other/b.md"), ap("5", "/Offices/Legal/c.md", { status: "approved" }),
  ], "/Offices/Legal");
  assert.equal(m.size, 1);
  assert.equal(m.get("/offices/legal/a.md").length, 2);
});

test("filterRows review and today", () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);
  const rows = [
    { path: "/a", updated_at: new Date(2026, 10, 9, 0, 0, 0).toISOString() },
    { path: "/B", updated_at: new Date(2026, 10, 8, 23, 59, 0).toISOString() },
  ];
  assert.equal(filterRows(rows, "all", new Map(), now).length, 2);
  assert.deepEqual(filterRows(rows, "today", new Map(), now).map((r) => r.path), ["/a"]);
  assert.deepEqual(filterRows(rows, "review", new Map([["/b", 1]]), now).map((r) => r.path), ["/B"]);
  assert.equal(filterRows(rows, "review", new Map(), now).length, 0);
});

test("proposedRows: exact folder only, skips existing and non-write", () => {
  const pend = pendingByPath([
    ap("1", "/Offices/Legal/new.md"), ap("2", "/Offices/Legal/Casos/deep.md"),
    ap("3", "/Offices/Legal/have.md"), ap("4", "/Offices/Legal/del.md", { context: { path: "/Offices/Legal/del.md", action: "delete" } }),
  ], "/Offices/Legal");
  const r = proposedRows(pend, "/Offices/Legal", new Set(["/Offices/Legal/have.md"]));
  assert.deepEqual(r.map((x) => x.name), ["new.md"]);
  assert.equal(r[0].approval.id, "1");
});

test("proposedRows accepts write_document and income_statement, not edit", () => {
  const mk = (id, name, action) => ap(id, "/Offices/Legal/" + name, { context: { path: "/Offices/Legal/" + name, action } });
  const pend = pendingByPath([mk("1", "d.md", "write_document"), mk("2", "p.md", "income_statement"), mk("3", "e.md", "edit"), mk("4", "m.md", "move")], "/Offices/Legal");
  assert.deepEqual(proposedRows(pend, "/Offices/Legal", new Set()).map((x) => x.name).sort(), ["d.md", "p.md"]);
});

test("defaultAgent", () => {
  assert.equal(defaultAgent(off({ agents: ["a1", "a2"] })), "a1");
  assert.equal(defaultAgent(off({ agents: [] })), undefined);
});

test("crumbs", () => {
  assert.deepEqual(crumbs("/Offices/Legal/Casos/2026", "/Offices/Legal"), [
    { name: "Legal", path: "/Offices/Legal" }, { name: "Casos", path: "/Offices/Legal/Casos" }, { name: "2026", path: "/Offices/Legal/Casos/2026" },
  ]);
  assert.equal(crumbs("/Offices/Legal", "/Offices/Legal").length, 1);
});

test("1000 rows filter fast", () => {
  const rows = Array.from({ length: 1000 }, (_, i) => ({ path: `/Offices/L/f${i}`, updated_at: new Date().toISOString() }));
  const pend = new Map(rows.filter((_, i) => i % 3 === 0).map((r) => [r.path.toLowerCase(), 1]));
  const t = performance.now();
  filterRows(rows, "review", pend, new Date()); filterRows(rows, "today", pend, new Date());
  assert.ok(performance.now() - t < 50);
});

test("pathKey matches the keys of pendingByPath", () => {
  assert.equal(pathKey("/Offices/Legal/A.md/"), "/offices/legal/a.md");
});

test("pendingUnder counts distinct paths strictly under a folder, any depth", () => {
  const pend = pendingByPath([
    ap("1", "/Offices/Legal/Casos/a.md"), ap("2", "/Offices/Legal/Casos/a.md"), ap("3", "/Offices/Legal/Casos/2026/b.md"),
    ap("4", "/Offices/Legal/c.md"), ap("5", "/Offices/Legal/CasosViejos/d.md"),
  ], "/Offices/Legal");
  assert.equal(pendingUnder(pend, "/Offices/Legal"), 4);
  assert.equal(pendingUnder(pend, "/offices/legal/casos/"), 2);
  assert.equal(pendingUnder(pend, "/Offices/Legal/Casos/2026/b.md"), 0);
});

test("findDepartment by id, case-insensitive and decoded; undefined when not mine", () => {
  const ds = [off({ id: "contabilidad" }), off({ id: "Ventas Norte" })];
  assert.equal(findDepartment(ds, "Contabilidad")?.id, "contabilidad");
  assert.equal(findDepartment(ds, "Ventas%20Norte")?.id, "Ventas Norte");
  assert.equal(findDepartment(ds, "legal"), undefined);
  assert.equal(findDepartment(ds, "%E0%A4%A"), undefined); // bad escape: no throw
});

test("deptView: what each filter shows and the review count", () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);
  const today = new Date(2026, 10, 9, 9, 0, 0).toISOString();
  const old = new Date(2026, 10, 1, 9, 0, 0).toISOString();
  const pend = pendingByPath([ap("1", "/Offices/L/a.md"), ap("2", "/Offices/L/Sub/x.md"), ap("3", "/Offices/L/new.md")], "/Offices/L");
  const folders = [{ path: "/Offices/L/Sub" }, { path: "/Offices/L/Empty" }];
  const files = [{ path: "/Offices/L/a.md", updated_at: old }, { path: "/Offices/L/b.md", updated_at: today }];
  const proposed = proposedRows(pend, "/Offices/L", new Set(files.map((f) => f.path)));
  const all = deptView({ folder: "/Offices/L", folders, files, proposed, pending: pend, now }, "all");
  assert.equal(all.folders.length, 2); assert.equal(all.files.length, 2); assert.equal(all.proposed.length, 1);
  assert.equal(all.reviewCount, 3);
  const rev = deptView({ folder: "/Offices/L", folders, files, proposed, pending: pend, now }, "review");
  assert.deepEqual(rev.folders.map((f) => f.path), ["/Offices/L/Sub"]);
  assert.deepEqual(rev.files.map((f) => f.path), ["/Offices/L/a.md"]);
  assert.equal(rev.proposed.length, 1);
  const tod = deptView({ folder: "/Offices/L", folders, files, proposed, pending: pend, now }, "today");
  assert.equal(tod.folders.length, 0); assert.equal(tod.proposed.length, 0);
  assert.deepEqual(tod.files.map((f) => f.path), ["/Offices/L/b.md"]);
});

test("lastChange: relative under a day, a date after", () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);
  assert.deepEqual(lastChange(new Date(2026, 10, 9, 14, 0, 0).toISOString(), now), { relative: true });
  assert.deepEqual(lastChange(new Date(2026, 10, 7, 14, 0, 0).toISOString(), now), { relative: false });
  assert.deepEqual(lastChange("nope", now), { relative: false });
});
