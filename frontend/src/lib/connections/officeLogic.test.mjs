// Unit tests of the connections of a department (D3) (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { agentFitsConn, canCoSignWrite, canCreateConn, canManageConn, connOffice, officeChoices } from "./officeLogic.ts";

test("General is no office", () => {
  assert.equal(connOffice({}), "");
  assert.equal(connOffice({ office_id: "general" }), "");
  assert.equal(connOffice({ office_id: "legal" }), "legal");
});

test("only the office's agents fit an office connection", () => {
  assert.equal(agentFitsConn("", "legal"), true);
  assert.equal(agentFitsConn("", undefined), true);
  assert.equal(agentFitsConn("legal", "legal"), true);
  assert.equal(agentFitsConn("legal", ""), false);
  assert.equal(agentFitsConn("legal", undefined), false);
  assert.equal(agentFitsConn("legal", "ventas"), false);
});

test("the server decides who manages; without it, the admins", () => {
  assert.equal(canManageConn({ can_manage: true }, false), true);
  assert.equal(canManageConn({ can_manage: false }, true), false);
  assert.equal(canManageConn({}, true), true);
  assert.equal(canManageConn({}, false), false);
});

const offices = [
  { id: "general", name: "General", general: true, can_manage: true },
  { id: "legal", name: "Legal", can_manage: true },
  { id: "ventas", name: "Ventas", can_manage: false },
];

test("an admin gives a connection to the company or any office", () => {
  assert.deepEqual(officeChoices(offices, true).map((o) => o.id), ["", "legal", "ventas"]);
});

test("a responsible gives it only to the offices they lead", () => {
  assert.deepEqual(officeChoices(offices, false, "legal").map((o) => o.id), ["legal"]);
  assert.deepEqual(officeChoices(offices, false, "ventas").map((o) => o.id), ["legal", "ventas"]);
});

test("the second human of a write grant: an admin, or the responsible of the connection's department", () => {
  assert.equal(canCoSignWrite({ can_manage: true }, true), true);
  assert.equal(canCoSignWrite({ office_id: "legal", can_manage: true }, false), true);
  assert.equal(canCoSignWrite({ can_manage: true }, false), false); // a company connection: admins only
  assert.equal(canCoSignWrite({ office_id: "legal", can_manage: false }, false), false);
});

test("creating needs an admin or the responsible of some office", () => {
  assert.equal(canCreateConn(offices, true), true);
  assert.equal(canCreateConn(offices, false), true);
  assert.equal(canCreateConn([offices[0], offices[2]], false), false);
});
