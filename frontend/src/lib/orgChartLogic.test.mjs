// Unit tests of the organization screens' logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  bossCandidates, descendantIds, detectCycle, flattenDepartments, isOpenAt, narrowTools, parentOptions, parseBudget, parseClock, ruleIssue,
  toggleDay, validateDepartment, validateSchedule, wallClock, wouldCycle,
} from "./orgChartLogic.ts";

const D = (id, name, parent_id = "") => ({ id, name, parent_id });
const deps = [D("hq", "HQ"), D("fin", "Finance", "hq"), D("tax", "Tax", "fin"), D("ops", "Ops", "hq")];

test("detectCycle finds self loops, longer loops and ignores trees", () => {
  assert.equal(detectCycle({}), null);
  assert.equal(detectCycle({ a: "b", b: "c", c: "" }), null);
  assert.deepEqual(detectCycle({ a: "a" }), ["a", "a"]);
  assert.deepEqual(detectCycle({ a: "b", b: "a" }), ["a", "b", "a"]);
  assert.deepEqual(detectCycle({ x: "a", a: "b", b: "c", c: "a" }), ["a", "b", "c", "a"]);
  assert.equal(detectCycle({ a: "r", b: "r", c: "a", d: "a" }), null);
});

test("wouldCycle and descendants guard the department tree", () => {
  assert.equal(wouldCycle(deps, "hq", "tax"), true);
  assert.equal(wouldCycle(deps, "fin", "fin"), true);
  assert.equal(wouldCycle(deps, "tax", "ops"), false);
  assert.deepEqual([...descendantIds(deps, "fin")].sort(), ["fin", "tax"]);
});

test("flattenDepartments lists parents before children with depth", () => {
  assert.deepEqual(flattenDepartments(deps).map((d) => `${d.depth}:${d.id}`), ["0:hq", "1:fin", "2:tax", "1:ops"]);
  // an orphan (unknown parent) is shown at the top instead of disappearing
  assert.deepEqual(flattenDepartments([D("a", "A", "ghost")]).map((d) => d.id), ["a"]);
});

test("parentOptions never offers the department itself or anything below it", () => {
  assert.deepEqual(parentOptions(deps, "fin").map((d) => d.id), ["hq", "ops"]);
  assert.deepEqual(parentOptions(deps).map((d) => d.id), ["hq", "fin", "tax", "ops"]);
});

test("parentOptions respects the depth limit", () => {
  const chain = [];
  let parent = "";
  for (let i = 0; i < 8; i++) { chain.push(D(`d${i}`, `D${i}`, parent)); parent = `d${i}`; }
  assert.ok(!parentOptions(chain).some((d) => d.id === "d7"), "a new child of the 8th level would be the 9th");
  assert.ok(parentOptions(chain).some((d) => d.id === "d6"));
});

test("validateDepartment", () => {
  assert.equal(validateDepartment({ name: "Legal", parent_id: "hq", cost_center: "CC-1" }, deps), null);
  assert.equal(validateDepartment({ name: "  ", parent_id: "", cost_center: "" }, deps), "name");
  assert.equal(validateDepartment({ name: "x".repeat(81), parent_id: "", cost_center: "" }, deps), "name_long");
  assert.equal(validateDepartment({ name: "Legal", parent_id: "", cost_center: "no spaces" }, deps), "cost_center");
  assert.equal(validateDepartment({ name: "finance", parent_id: "", cost_center: "" }, deps), "name_taken");
  assert.equal(validateDepartment({ name: "Finance", parent_id: "", cost_center: "" }, deps, "fin"), null, "renaming to itself is fine");
  assert.equal(validateDepartment({ name: "HQ", parent_id: "tax", cost_center: "" }, deps, "hq"), "parent_cycle");
});

test("parseClock and validateSchedule", () => {
  assert.equal(parseClock("09:30"), 570);
  assert.equal(parseClock("24:00"), 1440);
  assert.equal(parseClock("24:30"), null);
  assert.equal(parseClock("9:00"), null);
  const ok = { enabled: true, timezone: "America/New_York", days: [1, 2, 3], start: "09:00", end: "17:00" };
  assert.equal(validateSchedule(ok), null);
  assert.equal(validateSchedule({ ...ok, enabled: false, timezone: "nope" }), null, "disabled schedules are not validated");
  assert.equal(validateSchedule({ ...ok, timezone: "Mars/Olympus" }), "timezone");
  assert.equal(validateSchedule({ ...ok, days: [] }), "days");
  assert.equal(validateSchedule({ ...ok, days: [1, 1] }), "days");
  assert.equal(validateSchedule({ ...ok, start: "25:00" }), "clock");
  assert.equal(validateSchedule({ ...ok, start: "24:00", end: "08:00" }), "clock");
  assert.equal(validateSchedule({ ...ok, end: "09:00" }), "same_time");
  assert.deepEqual(toggleDay([1, 3], 2), [1, 2, 3]);
  assert.deepEqual(toggleDay([1, 2, 3], 2), [1, 3]);
});

const at = (s) => new Date(s);
const ny = { enabled: true, timezone: "America/New_York", days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00" };

test("wallClock follows the timezone and daylight saving", () => {
  assert.deepEqual(wallClock(at("2026-03-06T14:00:00Z"), "America/New_York"), { day: 5, minutes: 9 * 60 }); // Friday, EST
  assert.deepEqual(wallClock(at("2026-03-09T13:00:00Z"), "America/New_York"), { day: 1, minutes: 9 * 60 }); // Monday, EDT
  assert.deepEqual(wallClock(at("2026-03-09T03:00:00Z"), "America/Sao_Paulo"), { day: 1, minutes: 0 });
  assert.deepEqual(wallClock(at("2026-03-09T02:59:00Z"), "America/Sao_Paulo"), { day: 0, minutes: 23 * 60 + 59 });
});

test("isOpenAt matches the backend across the daylight saving change", () => {
  const cases = [
    ["2026-03-06T13:30:00Z", false], // Friday 08:30 EST
    ["2026-03-06T14:00:00Z", true],  // Friday 09:00 EST
    ["2026-03-09T12:59:00Z", false], // Monday 08:59 EDT
    ["2026-03-09T13:00:00Z", true],  // Monday 09:00 EDT
    ["2026-03-09T21:00:00Z", false], // Monday 17:00 EDT (end is exclusive)
    ["2026-11-02T13:59:00Z", false], // Monday after fall back 08:59 EST
    ["2026-11-02T14:00:00Z", true],
    ["2026-03-07T15:00:00Z", false], // Saturday
  ];
  for (const [when, open] of cases) assert.equal(isOpenAt(ny, at(when)), open, when);
});

test("isOpenAt handles overnight windows, round-the-clock and disabled schedules", () => {
  const night = { enabled: true, timezone: "UTC", days: [1, 2, 3, 4, 5], start: "22:00", end: "06:00" };
  assert.equal(isOpenAt(night, at("2026-03-09T21:59:00Z")), false);
  assert.equal(isOpenAt(night, at("2026-03-09T22:00:00Z")), true);
  assert.equal(isOpenAt(night, at("2026-03-10T05:59:00Z")), true);
  assert.equal(isOpenAt(night, at("2026-03-10T06:00:00Z")), false);
  assert.equal(isOpenAt(night, at("2026-03-14T03:00:00Z")), true, "Saturday small hours belong to Friday's window");
  assert.equal(isOpenAt(night, at("2026-03-15T03:00:00Z")), false);
  const all = { enabled: true, timezone: "Asia/Tokyo", days: [0, 1, 2, 3, 4, 5, 6], start: "00:00", end: "24:00" };
  assert.equal(isOpenAt(all, at("2026-07-01T23:59:00Z")), true);
  assert.equal(isOpenAt({ ...ny, enabled: false }, at("2026-03-07T15:00:00Z")), true);
  assert.equal(isOpenAt({ ...ny, timezone: "Mars/Olympus" }, at("2026-03-07T15:00:00Z")), true, "a broken schedule never stops an agent");
});

test("narrowTools can only keep tools of the role", () => {
  assert.deepEqual(narrowTools(["fs", "ledger", "spreadsheet"], ["spreadsheet", "crm", "fs"]), ["fs", "spreadsheet"]);
  assert.deepEqual(narrowTools(["fs"], []), []);
});

test("bossCandidates hides whoever would close a reporting loop", () => {
  const edges = { "agent:a": "agent:b", "agent:b": "person:owner" };
  const all = ["agent:a", "agent:b", "agent:c", "person:owner"];
  assert.deepEqual(bossCandidates(edges, "person:owner", all), ["agent:c"]);
  assert.deepEqual(bossCandidates(edges, "agent:c", all), ["agent:a", "agent:b", "person:owner"]);
});

test("parseBudget accepts blank (default), decimals with comma and rejects negatives", () => {
  assert.deepEqual(parseBudget(""), { value: null, ok: true });
  assert.deepEqual(parseBudget("12,5"), { value: 12.5, ok: true });
  assert.equal(parseBudget("-1").ok, false);
  assert.equal(parseBudget("abc").ok, false);
  assert.equal(parseBudget("2000000").ok, false);
});

test("ruleIssue", () => {
  const any = { type: "any", id: "" };
  assert.equal(ruleIssue({ from: any, to: any, effect: "allow", require_approval: true }), null);
  assert.equal(ruleIssue({ from: { type: "department", id: "" }, to: any, effect: "allow", require_approval: false }), "from");
  assert.equal(ruleIssue({ from: any, to: { type: "agent", id: "" }, effect: "allow", require_approval: false }), "to");
  assert.equal(ruleIssue({ from: any, to: any, effect: "deny", require_approval: true }), "deny_approval");
});
