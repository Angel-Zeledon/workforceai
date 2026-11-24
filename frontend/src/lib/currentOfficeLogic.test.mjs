// Run: node --test src/lib/currentOfficeLogic.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { ALL_OFFICES, agentInOffice, busyElsewhere, filterAgentIds, filterByOffice, inOffice, officeChoices, officeOfApproval, resolveCurrent, sendOfficeId, visitSpots, visitorIds } from "./currentOfficeLogic.ts";

const off = (id, o = {}) => ({ id, name: id[0].toUpperCase() + id.slice(1), can_enter: true, ...o });
const offices = [off("direccion", { can_enter: false, visibility: "private" }), off("legal"), off("general", { general: true }), off("ventas")];

test("one office: no selector", () => {
  assert.equal(officeChoices([off("general", { general: true })]), null);
  assert.equal(resolveCurrent("legal", [off("general")]), ALL_OFFICES);
});
test("choices: general, mine, closed last with lock", () => {
  const c = officeChoices(offices);
  assert.deepEqual(c.map((x) => x.id), ["general", "legal", "ventas", "direccion"]);
  assert.equal(c[3].locked, true);
});
test("stored office that vanished, is closed or is all resolves to all", () => {
  assert.equal(resolveCurrent("legal", offices), "legal");
  assert.equal(resolveCurrent("nope", offices), ALL_OFFICES);
  assert.equal(resolveCurrent("direccion", offices), ALL_OFFICES);
  assert.equal(resolveCurrent(null, offices), ALL_OFFICES);
});
test("items without office_id are General", () => {
  assert.equal(inOffice("general", undefined), true);
  assert.equal(inOffice("legal", undefined), false);
  assert.equal(inOffice("all", "legal"), true);
  const rows = [{ o: "legal" }, {}, { o: "ventas" }];
  assert.equal(filterByOffice(rows, "general", (r) => r.o).length, 1);
  assert.equal(filterByOffice(rows, "all", (r) => r.o).length, 3);
});
test("agents: base, accepted also-in, not pending", () => {
  const base = { a: "legal", b: "ventas", c: "ventas", d: undefined };
  const also = { b: [{ id: "legal", pending: false }], c: [{ id: "legal", pending: true }] };
  assert.deepEqual(filterAgentIds(["a", "b", "c", "d"], "legal", (i) => base[i], also), ["a", "b"]);
  assert.deepEqual(filterAgentIds(["a", "d"], "general", (i) => base[i], also), ["d"]);
  assert.equal(agentInOffice("all", "x"), true);
  assert.deepEqual(visitorIds(["a", "b", "c"], "legal", (i) => base[i], also), ["b"]);
  assert.deepEqual(visitorIds(["a", "b"], "all", (i) => base[i], also), []);
});
test("approvals take the office through task and request", () => {
  const tasks = { t1: { request_id: "r1" }, t2: { office_id: "ventas" } };
  const reqs = { r1: { office_id: "legal" } };
  assert.equal(officeOfApproval({ task_id: "t1" }, tasks, reqs), "legal");
  assert.equal(officeOfApproval({ task_id: "t2" }, tasks, reqs), "ventas");
  assert.equal(officeOfApproval({ task_id: "zz" }, tasks, reqs), "general");
  assert.equal(officeOfApproval({ office_id: "legal" }, tasks, reqs), "legal");
});
test("what a message is sent with", () => {
  assert.equal(sendOfficeId("legal", "ventas"), "legal");
  assert.equal(sendOfficeId("all", "ventas"), "ventas");
  assert.equal(sendOfficeId("all", ""), "general");
});
test("busy elsewhere: named, hidden or none", () => {
  assert.deepEqual(busyElsewhere("ventas", "legal", offices), { kind: "named", name: "Ventas" });
  assert.deepEqual(busyElsewhere("direccion", "legal", offices), { kind: "hidden" });
  assert.deepEqual(busyElsewhere("legal", "legal", offices), { kind: "none" });
  assert.deepEqual(busyElsewhere(null, "legal", offices), { kind: "none" });
});
test("visit spots skip used places", () => {
  const s = visitSpots([{ x: 0, z: 0 }, { x: 0.5, z: 0 }, { x: 3, z: 0 }, { x: 6, z: 0 }], [{ x: 0, z: 0 }], 2);
  assert.deepEqual(s, [{ x: 3, z: 0 }, { x: 6, z: 0 }]);
  assert.deepEqual(visitSpots([{ x: 1, z: 1 }], [], 0), []);
});
