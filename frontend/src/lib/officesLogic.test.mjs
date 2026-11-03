// Unit tests of the E2 offices logic (run: node --test src/lib/officesLogic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import { accessIssue, addableMembers, visibilityKey, convertibleDepartments, directoryGroups, layoutChoices, noteIssue, officeBadge, sortOffices, upsertShare } from "./officesLogic.ts";

const office = (o) => ({ visibility: "open", general: false, leads: [], members: [], member_count: 0, agents: [], can_enter: true, can_manage: false, is_member: false, is_lead: false, ...o });
const entry = (e) => ({ role: "", title: "", owner_kind: "office", owner_id: "general", owner_name: "General", office_id: "general", office_name: "General",
  visibility: "office", shares: [], explicit: false, rights: { see: true, use: true, edit: false }, ...e });
const me = { user_id: "u-ana", offices: ["general", "d-legal"] };

test("officeBadge tells General, a closed door and an open office apart", () => {
  assert.equal(officeBadge({ general: true, visibility: "open" }), "general");
  assert.equal(officeBadge({ general: false, visibility: "private" }), "private");
  assert.equal(officeBadge({ general: false, visibility: "open" }), "open");
});

test("sortOffices puts General first, then mine, then the others by name", () => {
  const got = sortOffices([office({ id: "z", name: "Ventas" }), office({ id: "l", name: "Legal", is_member: true }), office({ id: "general", name: "General", general: true }), office({ id: "a", name: "Admin" })]);
  assert.deepEqual(got.map((o) => o.id), ["general", "l", "a", "z"]);
});

test("convertibleDepartments and addableMembers leave out what is already there", () => {
  assert.deepEqual(convertibleDepartments({ departments: [{ id: "d1", name: "Legal", office: true }, { id: "d2", name: "Ventas", office: false }] }), [{ id: "d2", name: "Ventas" }]);
  const people = [{ user_id: "a", name: "Ana" }, { user_id: "b", name: "Luis" }];
  assert.deepEqual(addableMembers(people, { members: [{ user_id: "a", name: "Ana" }] }), [{ user_id: "b", name: "Luis" }]);
});

test("directoryGroups: mine, my office, shared with me, the company, and the ones I only see", () => {
  const groups = directoryGroups([
    entry({ id: "1", name: "Sofía", owner_kind: "person", owner_id: "u-ana", visibility: "private" }),
    entry({ id: "2", name: "Elena", owner_id: "d-legal", office_id: "d-legal", office_name: "Legal" }),
    entry({ id: "3", name: "Nadia", owner_kind: "person", owner_id: "u-luis", visibility: "people", shares: [{ user_id: "u-ana", level: "chat" }] }),
    entry({ id: "4", name: "Valeria" }),
    entry({ id: "5", name: "Pablo", office_id: "d-sales", rights: { see: true, use: false, edit: false } }),
    entry({ id: "6", name: "Carla", office_id: "d-legal", visibility: "directory" }),
  ], me);
  assert.deepEqual(groups.map((g) => [g.group, g.items.map((i) => i.id)]), [["mine", ["1"]], ["office", ["2"]], ["shared", ["3"]], ["company", ["6", "4"]], ["visible", ["5"]]]);
});

test("an admin who is not in Legal finds Legal's agents under other offices, usable", () => {
  const admin = { user_id: "u-owner", offices: ["general"] };
  const groups = directoryGroups([entry({ id: "e", name: "Elena", owner_id: "d-legal", office_id: "d-legal" }), entry({ id: "v", name: "Valeria" })], admin);
  assert.deepEqual(groups.map((g) => [g.group, g.items.map((i) => i.id)]), [["company", ["v"]], ["visible", ["e"]]]);
});

test("visibilityKey: General's office agents are for the whole company", () => {
  assert.equal(visibilityKey({ office_id: "general", owner_kind: "office", visibility: "office" }), "offices.badge.general");
  assert.equal(visibilityKey({ office_id: "d", owner_kind: "office", visibility: "office" }), "offices.vis.office");
  assert.equal(visibilityKey({ office_id: "general", owner_kind: "person", visibility: "private" }), "offices.vis.private");
});

test("directoryGroups filters by name, office or owner without accents", () => {
  const list = [entry({ id: "1", name: "Sofía" }), entry({ id: "2", name: "Elena", office_name: "Contabilidad" })];
  assert.deepEqual(directoryGroups(list, me, "sofia").flatMap((g) => g.items.map((i) => i.id)), ["1"]);
  assert.deepEqual(directoryGroups(list, me, "CONTA").flatMap((g) => g.items.map((i) => i.id)), ["2"]);
  assert.deepEqual(directoryGroups(list, me, "nadie"), []);
});

test("accessIssue: an owner needs an id, the ladder has four steps, a personal agent shared with people needs someone", () => {
  assert.equal(accessIssue({ owner_kind: "person", owner_id: "" }), "owner");
  assert.equal(accessIssue({ visibility: "everyone" }), "visibility");
  assert.equal(accessIssue({ owner_kind: "person", owner_id: "u", visibility: "people", shares: [] }), "noShares");
  assert.equal(accessIssue({ owner_kind: "office", owner_id: "d", visibility: "people", shares: [] }), null);
  assert.equal(accessIssue({ visibility: "directory" }), null);
});

test("upsertShare keeps one share per person", () => {
  const got = upsertShare([{ user_id: "a", level: "chat" }, { user_id: "b", level: "chat" }], { user_id: "a", level: "edit" });
  assert.deepEqual(got, [{ user_id: "b", level: "chat" }, { user_id: "a", level: "edit" }]);
});

test("noteIssue refuses empty and too long notes", () => {
  assert.equal(noteIssue("   "), "empty");
  assert.equal(noteIssue("x".repeat(1001)), "long");
  assert.equal(noteIssue("Usamos la plantilla de la socia"), null);
});

test("layoutChoices: no picker with only General; private offices I am not in are locked", () => {
  assert.equal(layoutChoices([office({ id: "general", name: "General", general: true })]), null);
  const got = layoutChoices([office({ id: "general", name: "General", general: true }), office({ id: "d", name: "Legal", visibility: "private", can_enter: false })]);
  assert.deepEqual(got, [{ id: "general", name: "General", locked: false }, { id: "d", name: "Legal", locked: true }]);
});
