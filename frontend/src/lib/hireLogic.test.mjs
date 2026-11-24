// Run: node --test src/lib/hireLogic.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { emptyDraft, firstIssueStep, hireBody, hireError, keptSkills, pickOffice, pickPosition, pickTemplate, stepIssue, toggleRemoved, visibilityChoices } from "./hireLogic.ts";

test("each step says what is missing; going back needs nothing", () => {
  const d = emptyDraft();
  assert.equal(stepIssue(d, 1), "office");
  assert.equal(stepIssue({ ...d, office: "general" }, 1), null);
  assert.equal(stepIssue(d, 2), "template");
  assert.equal(stepIssue({ ...d, visibility: "people" }, 3), "people");
  assert.equal(stepIssue({ ...d, name: "x".repeat(41) }, 3), "name");
  assert.equal(firstIssueStep({ ...d, office: "legal" }), 2);
  assert.equal(firstIssueStep({ ...d, office: "legal", template: "t" }), null);
});

test("changing the office resets position, removed skills and an unavailable visibility", () => {
  let d = { ...emptyDraft("legal"), template: "t", position: "p1", removed: ["s1"], visibility: "directory" };
  d = pickOffice(d, "general");
  assert.deepEqual([d.position, d.removed, d.visibility, d.template], ["", [], "office", "t"]);
  assert.deepEqual(visibilityChoices("general"), ["office", "people"]);
  assert.deepEqual(visibilityChoices("legal"), ["office", "people", "directory"]);
  assert.equal(pickOffice(d, "general"), d);
  assert.deepEqual(pickPosition({ ...d, removed: ["a"] }, "p2").removed, []);
  assert.deepEqual(pickTemplate({ ...d, removed: ["a"] }, "t").removed, ["a"]);
});

test("removed suggestions are not sent; the body omits General and empty things", () => {
  const sug = [{ id: "a" }, { id: "b" }, { id: "c" }];
  let d = { ...emptyDraft("legal"), template: "t", position: "p", name: " Ana " };
  d = { ...d, removed: toggleRemoved(d.removed, "b") };
  assert.deepEqual(keptSkills(sug, d).map((s) => s.id), ["a", "c"]);
  assert.deepEqual(toggleRemoved(["b"], "b"), []);
  const b = hireBody(d, sug, "es");
  assert.deepEqual([b.office_id, b.position_id, b.skills, b.name, b.visibility, b.shares], ["legal", "p", ["a", "c"], "Ana", "office", undefined]);
  const g = hireBody({ ...emptyDraft("general"), template: "t", position: "p", visibility: "people", people: ["u1"] }, [], "en");
  assert.deepEqual([g.office_id, g.position_id, g.skills, g.shares], [undefined, undefined, undefined, [{ user_id: "u1", level: "chat" }]]);
});

test("a refused hire points at its step", () => {
  const d = { ...emptyDraft("legal"), template: "t" };
  assert.equal(hireError(403, undefined, d).step, 1);
  assert.equal(hireError(400, "position_other_office", d).step, 2);
  assert.equal(hireError(404, undefined, { ...d, position: "p" }).step, 2);
  assert.equal(hireError(404, undefined, d).step, 1);
  assert.equal(hireError(409, undefined, d).step, 3);
  assert.equal(hireError(500, undefined, d).key, "hire.error");
});
