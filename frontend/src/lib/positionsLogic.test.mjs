// Unit tests of the positions rules (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  accessSourceKey, clampToSuperior, covers, descendantsOf, errorKey, exceeds, exceedsDetails, impactLines, isDirty, levelOf,
  maxLevelFor, orderTree, proposeFromSuperior, relativePath, setEntry, superiorChoices, toggleSkill, viaKey,
} from "./positionsLogic.ts";

const B = { office_folder: "/Offices/Contabilidad", shared: "/Shared", shared_in: ["/Offices/Legal/Contratos"] };
const pos = (id, name, reports_to = "", o = {}) => ({ id, office_id: "o", name, reports_to, skills: [], library: [], template: "", occupants: [], created_by: "", created_at: "", updated_by: "", updated_at: "", ...o });

test("covers ignores case and trailing slashes and needs a real folder boundary", () => {
  assert.equal(covers("/Offices/Contabilidad/", "/offices/contabilidad/Bancos"), true);
  assert.equal(covers("/Shared", "/SharedX"), false);
  assert.equal(covers("/Shared", "/Shared"), true);
});

test("an empty library is the base: department Edit, Shared and shared-in Read, the rest none", () => {
  assert.equal(levelOf([], "/Offices/Contabilidad/Bancos", B), "edit");
  assert.equal(levelOf([], "/Shared/x", B), "read");
  assert.equal(levelOf([], "/Offices/Legal/Contratos/a", B), "read");
  assert.equal(levelOf([], "/Offices/Ventas", B), "none");
});

test("with a library the deepest entry wins and what no entry covers is none", () => {
  const lib = [{ path: "/Offices/Contabilidad", level: "read" }, { path: "/Offices/Contabilidad/Bancos", level: "edit" }, { path: "/Offices/Contabilidad/Bancos/Privado", level: "none" }];
  assert.equal(levelOf(lib, "/Offices/Contabilidad/Bancos/x.csv", B), "edit");
  assert.equal(levelOf(lib, "/Offices/Contabilidad/Bancos/Privado/y", B), "none");
  assert.equal(levelOf(lib, "/Offices/Contabilidad/Otros", B), "read");
  assert.equal(levelOf(lib, "/Shared", B), "none");
});

test("exceeds lists skills and folders a subordinate has above its superior", () => {
  const sup = { skills: ["a", "b"], library: [{ path: "/Offices/Contabilidad/Bancos", level: "read" }] };
  const sub = { skills: ["a", "c"], library: [{ path: "/Offices/Contabilidad/Bancos", level: "edit" }, { path: "/Offices/Contabilidad/Nomina", level: "read" }, { path: "/Offices/Contabilidad/Bancos/Viejo", level: "none" }] };
  const e = exceeds(sup, sub, B);
  assert.deepEqual(e.skills, ["c"]);
  assert.deepEqual(e.folders, [{ path: "/Offices/Contabilidad/Bancos", level: "edit" }, { path: "/Offices/Contabilidad/Nomina", level: "read" }]);
});

test("a subordinate with the same or less is fine; an exclusion never exceeds", () => {
  const sup = { skills: ["a"], library: [{ path: "/Offices/Contabilidad", level: "edit" }] };
  const sub = { skills: ["a"], library: [{ path: "/Offices/Contabilidad", level: "read" }, { path: "/Offices/Contabilidad/X", level: "none" }] };
  assert.deepEqual(exceeds(sup, sub, B), { skills: [], folders: [] });
});

test("a superior that excludes a subfolder blocks a subordinate that covers it from above", () => {
  const sup = { skills: [], library: [{ path: "/Offices/Contabilidad", level: "edit" }, { path: "/Offices/Contabilidad/Secreto", level: "none" }] };
  const sub = { skills: [], library: [{ path: "/Offices/Contabilidad", level: "read" }] };
  assert.deepEqual(exceeds(sup, sub, B).folders, [{ path: "/Offices/Contabilidad/Secreto", level: "read" }]);
});

test("an empty library is the base on both sides, like the server", () => {
  const sup = { skills: [], library: [{ path: "/Shared", level: "read" }] };
  assert.ok(exceeds(sup, { skills: [], library: [] }, B).folders.length > 0, "no library under a narrower manager exceeds");
  assert.deepEqual(exceeds({ skills: [], library: [] }, { skills: [], library: [{ path: "/Shared/x", level: "read" }] }, B), { skills: [], folders: [] });
});

test("clampToSuperior keeps the superior's exclusions and never ends as 'no library'", () => {
  const sup = { skills: [], library: [{ path: "/Offices/Contabilidad", level: "edit" }, { path: "/Offices/Contabilidad/Secreto", level: "none" }] };
  const c = clampToSuperior(sup, { skills: [], library: [{ path: "/Offices/Contabilidad", level: "read" }] }, B);
  assert.deepEqual(exceeds(sup, c, B), { skills: [], folders: [] });
  const none = clampToSuperior({ skills: [], library: [{ path: "/Shared", level: "none" }] }, { skills: [], library: [] }, B);
  assert.ok(none.library.length > 0);
  assert.deepEqual(exceeds({ skills: [], library: [{ path: "/Shared", level: "none" }] }, none, B), { skills: [], folders: [] });
});

test("proposeFromSuperior keeps the folders, lowers Edit to Read, keeps exclusions and ticks no skill", () => {
  const sup = { skills: ["a"], library: [{ path: "/Offices/Contabilidad", level: "edit" }, { path: "/Offices/Contabilidad/X", level: "none" }, { path: "/Shared", level: "read" }] };
  assert.deepEqual(proposeFromSuperior(sup, B), { skills: [], library: [{ path: "/Offices/Contabilidad", level: "read" }, { path: "/Offices/Contabilidad/X", level: "none" }, { path: "/Shared", level: "read" }] });
});

test("proposeFromSuperior with a superior that has no library starts from the base, read-only", () => {
  const p = proposeFromSuperior({ skills: ["a"], library: [] }, B);
  assert.deepEqual(p.skills, []);
  assert.deepEqual(p.library.map((e) => e.level), ["read", "read", "read"]);
  assert.equal(p.library[0].path, "/Offices/Contabilidad");
});

test("clampToSuperior removes what exceeds and the result no longer exceeds", () => {
  const sup = { skills: ["a"], library: [{ path: "/Offices/Contabilidad/Bancos", level: "read" }] };
  const draft = { skills: ["a", "z"], library: [{ path: "/Offices/Contabilidad/Bancos", level: "edit" }, { path: "/Offices/Contabilidad/Nomina", level: "read" }] };
  const c = clampToSuperior(sup, draft, B);
  assert.deepEqual(c.skills, ["a"]);
  assert.deepEqual(c.library, [{ path: "/Offices/Contabilidad/Bancos", level: "read" }]);
  assert.deepEqual(exceeds(sup, c, B), { skills: [], folders: [] });
});

test("maxLevelFor: bounded by the superior, and folders shared into the department are read-only", () => {
  assert.equal(maxLevelFor(null, "/Offices/Contabilidad/x", B), "edit");
  assert.equal(maxLevelFor(null, "/Offices/Legal/Contratos", B), "read");
  assert.equal(maxLevelFor({ skills: [], library: [{ path: "/Offices/Contabilidad", level: "read" }] }, "/Offices/Contabilidad/x", B), "read");
  assert.equal(maxLevelFor({ skills: [], library: [{ path: "/Offices/Contabilidad", level: "read" }] }, "/Shared", B), "none");
});

test("setEntry replaces by path ignoring case, removes with null and keeps order", () => {
  let lib = setEntry([], "/b", "read");
  lib = setEntry(lib, "/A", "edit");
  assert.deepEqual(lib.map((e) => e.path), ["/A", "/b"]);
  lib = setEntry(lib, "/B", "none");
  assert.deepEqual(lib, [{ path: "/A", level: "edit" }, { path: "/B", level: "none" }]);
  assert.deepEqual(setEntry(lib, "/a", null), [{ path: "/B", level: "none" }]);
});

test("toggleSkill and isDirty", () => {
  assert.deepEqual(toggleSkill(["a"], "b"), ["a", "b"]);
  assert.deepEqual(toggleSkill(["a", "b"], "a"), ["b"]);
  const p = pos("1", "Gerente", "", { skills: ["a", "b"], library: [{ path: "/x", level: "read" }] });
  assert.equal(isDirty(p, { name: " Gerente ", reports_to: "", skills: ["b", "a"], library: [{ path: "/X", level: "read" }] }), false);
  assert.equal(isDirty(p, { name: "Gerente", reports_to: "2", skills: ["a", "b"], library: p.library }), true);
});

test("relativePath names the part below its root", () => {
  assert.equal(relativePath("/Offices/Contabilidad/Bancos/2026", B), "Bancos/2026");
  assert.equal(relativePath("/Offices/Contabilidad", B), "");
});

test("orderTree puts the manager first and the subordinates indented below, siblings by name", () => {
  const rows = orderTree([pos("e", "Especialista", "g"), pos("a", "Asistente", "g"), pos("g", "Gerente"), pos("z", "Auditor externo"), pos("s", "Soporte", "a")]);
  assert.deepEqual(rows.map((r) => [r.position.id, r.depth]), [["z", 0], ["g", 0], ["a", 1], ["s", 2], ["e", 1]]);
});

test("orderTree survives a cycle and an orphan", () => {
  const rows = orderTree([pos("a", "A", "b"), pos("b", "B", "a"), pos("c", "C", "nope")]);
  assert.equal(rows.length, 3);
  assert.equal(new Set(rows.map((r) => r.position.id)).size, 3);
});

test("descendantsOf and superiorChoices never offer itself or its subordinates", () => {
  const ps = [pos("g", "Gerente"), pos("a", "Asistente", "g"), pos("s", "Soporte", "a"), pos("o", "Otro")];
  assert.deepEqual(descendantsOf(ps, "g").sort(), ["a", "s"]);
  assert.deepEqual(superiorChoices(ps, "g").map((p) => p.id), ["o"]);
  assert.deepEqual(superiorChoices(ps, null).length, 4);
});

test("error codes map to a message key; unknown ones to the generic key, a 404 to the department one", () => {
  assert.equal(errorKey("name_taken"), "pos.err.name_taken");
  assert.equal(errorKey("exceeds_superior", 409), "pos.err.exceeds_superior");
  assert.equal(errorKey("something_new", 500), "pos.err.generic");
  assert.equal(errorKey(undefined, 500), "pos.err.generic");
  assert.equal(errorKey(undefined, 404), "pos.err.notFoundOffice");
  assert.equal(errorKey("not_found", 404), "pos.err.notFoundOffice");
});

test("origin keys of skills and folder rules", () => {
  assert.equal(viaKey("agent"), "pos.via.agent");
  assert.equal(viaKey("position"), "pos.via.position");
  assert.equal(viaKey("role"), "pos.via.role");
  assert.equal(viaKey(undefined), "pos.via.agent");
  assert.equal(accessSourceKey("position"), "pos.src.position");
  assert.equal(accessSourceKey("manual"), "pos.src.manual");
  assert.equal(accessSourceKey("default"), "pos.src.default");
  assert.equal(accessSourceKey("default", true), "pos.src.fixed");
});

test("impactLines drops empty changes; exceedsDetails tolerates garbage", () => {
  const impact = { changed: [{ position_id: "a", name: "Asistente", skills_removed: ["x"], folders: [] }, { position_id: "b", name: "Otro", skills_removed: [], folders: [] }] };
  assert.deepEqual(impactLines(impact).map((c) => c.position_id), ["a"]);
  assert.deepEqual(impactLines(null), []);
  assert.deepEqual(exceedsDetails(null), { skills: [], folders: [] });
  assert.deepEqual(exceedsDetails({ skills: ["a", 3], folders: [{ path: "/x", level: "edit" }, 7] }), { skills: ["a"], folders: [{ path: "/x", level: "edit" }] });
});
