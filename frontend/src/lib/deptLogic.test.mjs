// Unit tests of the department drive logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { myDepartments, pendingByPath, filterRows, proposedRows, defaultAgent, crumbs, pathKey, pendingUnder, findDepartment, deptView, lastChange,
  folderFromParam, folderParam, usableAgents, sendErrorKey, msUntilMidnight, deptHref, needsReview, agentUnreviewed, withReviewed, TAG_REVIEWED, personNames, landOnDepartment } from "./deptLogic.ts";

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
  assert.equal(all.reviewCount, 3); // a.md, new.md and Sub/x.md
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

test("folderFromParam: relative to the root, never outside it", () => {
  const R = "/Offices/Contabilidad";
  assert.equal(folderFromParam(R, null), R);
  assert.equal(folderFromParam(R, ""), R);
  assert.equal(folderFromParam(R, "Bancos y conciliaciones"), R + "/Bancos y conciliaciones");
  assert.equal(folderFromParam(R, "/Clientes/Grupo Alfa/"), R + "/Clientes/Grupo Alfa");
  assert.equal(folderFromParam(R, "Clientes//Grupo Alfa"), R + "/Clientes/Grupo Alfa");
  assert.equal(folderFromParam(R, "../Legal"), R);
  assert.equal(folderFromParam(R, "Clientes/../../Legal"), R);
  assert.equal(folderFromParam(R, "./Clientes"), R);
  assert.equal(folderFromParam(R, String.raw`a\b`), R);
});

test("folderParam is the inverse of folderFromParam", () => {
  const R = "/Offices/Contabilidad";
  assert.equal(folderParam(R, R), "");
  assert.equal(folderParam(R, R + "/Clientes/Grupo Alfa"), "Clientes/Grupo Alfa");
  assert.equal(folderParam(R, "/offices/contabilidad/Impuestos"), "Impuestos");
  assert.equal(folderParam(R, "/Shared/x"), "");
  assert.equal(folderFromParam(R, folderParam(R, R + "/Impuestos")), R + "/Impuestos");
});

test("usableAgents: the office's agents the person may use; without a directory, all of them", () => {
  const dir = [{ id: "a", rights: { see: true, use: true, edit: false } }, { id: "b", rights: { see: true, use: false, edit: false } }];
  assert.deepEqual(usableAgents(["a", "b", "c"], dir), ["a"]);
  assert.deepEqual(usableAgents(["a", "b"], null), ["a", "b"]);
});

test("sendErrorKey maps the refusal to a clear text", () => {
  assert.equal(sendErrorKey(new Error("POST /messages -> 403")), "dept.ask.err.forbidden");
  const err = (status, code) => Object.assign(new Error(`POST /messages -> ${status}`), { status, code });
  assert.equal(sendErrorKey(err(400, "folder_invalid")), "dept.ask.err.folder");
  assert.equal(sendErrorKey(err(400)), "chat.sendError"); // another 400 (text too long...) is not the folder's fault
  // 404: the drive's not_found is the folder (missing or not visible); a 404 without that code is an unknown agent
  assert.equal(sendErrorKey(err(404, "not_found")), "dept.ask.err.folder");
  assert.equal(sendErrorKey(err(404)), "dept.ask.err.agent");
  assert.equal(sendErrorKey(new Error("POST /messages -> 404")), "dept.ask.err.agent");
  assert.equal(sendErrorKey(new Error("POST /messages -> 500")), "chat.sendError");
  assert.equal(sendErrorKey("boom"), "chat.sendError");
});

test("msUntilMidnight", () => {
  assert.equal(msUntilMidnight(new Date(2026, 10, 9, 23, 59, 0)), 60_000);
  assert.equal(msUntilMidnight(new Date(2026, 10, 9, 0, 0, 0)), 24 * 3600_000);
});

test("deptHref: the department and, below its root, the folder", () => {
  assert.equal(deptHref("contabilidad", ""), "/d/contabilidad");
  assert.equal(deptHref("Ventas Norte", "Clientes/Grupo Alfa"), "/d/Ventas%20Norte?carpeta=Clientes%2FGrupo%20Alfa");
});

test("needsReview: a pending approval, or an agent's last version nobody marked as reviewed", () => {
  const agent = { kind: "agent", id: "accounting" }, person = { kind: "human", id: "u1" };
  const f = (path, updated_by, tags = []) => ({ path, updated_by, tags });
  const none = new Map();
  assert.equal(needsReview(f("/Offices/L/a.md", agent), none), true);
  assert.equal(needsReview(f("/Offices/L/a.md", agent, ["cierre", "Revisado"]), none), false); // tags compare case-insensitively
  assert.equal(needsReview(f("/Offices/L/a.md", person), none), false);
  assert.equal(needsReview(f("/Shared/a.md", agent), none), false); // only department files
  assert.equal(needsReview(f("/Offices/L/a.md", person, [TAG_REVIEWED]), new Map([["/offices/l/a.md", 1]])), true); // pending wins
  assert.equal(needsReview({ path: "/Offices/L/a.md" }, none), false); // no author, no tags: nothing to review
  assert.equal(agentUnreviewed(f("/offices/l/x.md", agent, null)), true);
});

test("withReviewed adds the tag once and keeps the others", () => {
  assert.deepEqual(withReviewed(["cierre"]), ["cierre", "revisado"]);
  assert.deepEqual(withReviewed(["revisado"]), ["revisado"]);
  assert.deepEqual(withReviewed(undefined), ["revisado"]);
});

test("deptView: agent work counts and shows under Para revisar; the count is only what the list can show", () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);
  const at = new Date(2026, 10, 1).toISOString();
  const agent = { kind: "agent", id: "accounting" }, person = { kind: "human", id: "u1" };
  const pend = pendingByPath([
    ap("1", "/Offices/L/b.md"), // on an existing file
    ap("2", "/Offices/L/ghost.md", { context: { path: "/Offices/L/ghost.md", action: "move" } }), // no file, creates nothing: not shown, not counted
    ap("3", "/Offices/L/new.md", { context: { path: "/Offices/L/new.md", action: "write" } }), // proposed
    ap("4", "/Offices/L/Sub/x.md"), // inside a listed subfolder
    ap("5", "/Offices/L/Gone/y.md"), // inside a folder that is not listed
  ], "/Offices/L");
  const files = [
    { path: "/Offices/L/a.md", updated_by: agent, tags: [], updated_at: at },
    { path: "/Offices/L/b.md", updated_by: person, tags: [], updated_at: at },
    { path: "/Offices/L/c.md", updated_by: agent, tags: ["revisado"], updated_at: at },
    { path: "/Offices/L/d.md", updated_by: person, tags: [], updated_at: at },
  ];
  const folders = [{ path: "/Offices/L/Sub" }];
  const proposed = proposedRows(pend, "/Offices/L", new Set(files.map((f) => f.path)));
  const rev = deptView({ folder: "/Offices/L", folders, files, proposed, pending: pend, now }, "review");
  assert.deepEqual(rev.files.map((f) => f.path), ["/Offices/L/a.md", "/Offices/L/b.md"]);
  assert.equal(rev.proposed.length, 1);
  assert.equal(rev.reviewCount, 4); // a.md (agent) + b.md (pending) + new.md (proposed) + Sub/x.md
});

test("personNames: leads, members and the person reading, by user id", () => {
  const m = personNames({ leads: [{ user_id: "u1", name: "Ana" }], members: [{ user_id: "u1", name: "Ana" }, { user_id: "u2", name: " Beto " }, { user_id: "u3", name: "" }] },
    { id: "me", name: "Carla" });
  assert.equal(m.get("u1"), "Ana"); assert.equal(m.get("u2"), "Beto"); assert.equal(m.has("u3"), false); assert.equal(m.get("me"), "Carla");
  assert.equal(personNames({ leads: [], members: [] }, null).size, 0);
});

test("landOnDepartment: a company account entering the tab at / with a department", () => {
  const v = { firstEntry: true, path: "/", audience: "company", departments: 1 };
  assert.equal(landOnDepartment(v), true);
  assert.equal(landOnDepartment({ ...v, firstEntry: false }), false); // back at / later (from "Más")
  assert.equal(landOnDepartment({ ...v, path: "/files" }), false);
  assert.equal(landOnDepartment({ ...v, audience: "team" }), false);
  assert.equal(landOnDepartment({ ...v, audience: null }), false);
  assert.equal(landOnDepartment({ ...v, departments: 0 }), false);
});
