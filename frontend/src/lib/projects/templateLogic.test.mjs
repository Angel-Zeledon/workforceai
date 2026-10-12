// Unit tests of the customizable-template logic (run: npm run test:unit; Node >= 22.18 strips the TypeScript types).
import assert from "node:assert/strict";
import fs from "node:fs";
import { test } from "node:test";
import {
  addParam, addPhase, addTask, addWorkflow, blankState, decodeDep, encodeDep, expandTemplate, fromTemplate, issuesFromError, limitFromDetail,
  moveTask, nextKey, patchParam, patchTask, patchWorkflow, placeholders, removeParam, removeTask, removeWorkflow, setKind, slugParamKey, splitList, statsOf,
  summarize, toInput, usedKeys, validateLike, TPL_LIMITS, localIssues,
} from "./templateLogic.ts";
import { GIANT_TEMPLATES } from "./mockGiants.ts";

const giant = JSON.parse(fs.readFileSync(new URL("../../../../backend/internal/projects/giant_i18n.json", import.meta.url), "utf8"));
const tt = (k, vars = {}) => (giant.en[k] ?? k).replace(/\{(\w+)\}/g, (m, n) => (n in vars ? vars[n] : m));

test("keys: sequential, unique and valid", () => {
  const used = new Set(["t1", "t3"]);
  assert.equal(nextKey("t", used), "t2");
  assert.equal(nextKey("t", used), "t4");
  assert.ok(used.has("t4"));
  let s = blankState();
  assert.deepEqual([...usedKeys(s)].sort(), ["o1", "t1", "w1"]);
  s = addPhase(addWorkflow(addTask(s, 0, 0), 0), 0);
  const keys = [...usedKeys(s)];
  assert.equal(new Set(keys).size, keys.length);
  assert.ok(keys.every((k) => /^[A-Za-z][A-Za-z0-9]{0,31}$/.test(k)));
});

test("param keys from labels", () => {
  assert.equal(slugParamKey("Business units", []), "businessUnits");
  assert.equal(slugParamKey("Países y regiones", []), "paisesYRegiones");
  assert.equal(slugParamKey("Países", ["paises"]), "paises2");
  assert.equal(slugParamKey("Item name", []), "pItemName");
  assert.equal(slugParamKey("goal", []), "pGoal");
  assert.equal(slugParamKey("123", []), "param");
  assert.match(slugParamKey("x".repeat(60), []), /^[a-z][a-zA-Z0-9]{0,23}$/);
});

test("dependency encoding", () => {
  assert.equal(encodeDep("t1"), "t1");
  assert.equal(encodeDep("t1", "waves"), "t1^waves");
  assert.deepEqual(decodeDep("t1^waves"), { key: "t1", prev: "waves" });
  assert.deepEqual(decodeDep("t1"), { key: "t1" });
});

test("lists and placeholders", () => {
  assert.deepEqual(splitList("a, b;c\n d ,, "), ["a", "b", "c", "d"]);
  let s = addParam(blankState());
  s = patchParam(s, 0, { label: "Countries", type: "list" });
  assert.equal(s.params[0].key, "countries");
  assert.equal(s.params[0].itemName, "item");
  s = patchParam(s, 0, { itemName: "country" });
  assert.deepEqual(placeholders(s), ["{countries}"]);
  s = patchWorkflow(s, 0, 0, { repeat: ["countries"] });
  assert.deepEqual(placeholders(s, s.phases[0].workflows[0].repeat), ["{countries}", "{country}"]);
  // renaming the key follows the repeats; removing the param prunes them
  s = patchParam(s, 0, { key: "nations" });
  assert.deepEqual(s.phases[0].workflows[0].repeat, ["nations"]);
  s = removeParam(s, 0);
  assert.deepEqual(s.phases[0].workflows[0].repeat, []);
});

test("tree edits: limits, move and dependency pruning", () => {
  let s = blankState();
  s = addTask(s, 0, 0); s = addTask(s, 0, 0);
  const [a, b, c] = s.phases[0].workflows[0].tasks.map((t) => t.key);
  s = patchTask(s, 0, 0, 2, { deps: [a, b] });
  s = moveTask(s, 0, 0, 2, -1);
  assert.deepEqual(s.phases[0].workflows[0].tasks.map((t) => t.key), [a, c, b]);
  s = removeTask(s, 0, 0, 0);
  assert.deepEqual(s.phases[0].workflows[0].tasks.find((t) => t.key === c).deps, [b]);
  // out of range moves are no-ops
  assert.equal(moveTask(s, 0, 0, 0, -1).phases[0].workflows[0].tasks[0].key, c);
  // removing a workflow drops dependencies on its tasks
  s = addWorkflow(s, 0);
  const other = s.phases[0].workflows[1].tasks[0].key;
  s = patchTask(s, 0, 0, 0, { deps: [other] });
  s = removeWorkflow(s, 0, 1);
  assert.deepEqual(s.phases[0].workflows[0].tasks[0].deps, []);
  // backend limits
  let big = blankState();
  for (let i = 0; i < 40; i++) big = addPhase(big);
  assert.equal(big.phases.length, TPL_LIMITS.phases);
  let p = blankState();
  for (let i = 0; i < 20; i++) p = addParam(p);
  assert.equal(p.params.length, TPL_LIMITS.params);
});

test("kind change: gates default to approval on", () => {
  let s = addTask(blankState(), 0, 0);
  const t = setKind(s.phases[0].workflows[0].tasks[0], "gate");
  assert.equal(t.approval.on, true);
  assert.equal(setKind(t, "wait").approval.on, false);
});

function sample() {
  let s = blankState();
  s = { ...s, name: " Budget ", description: "d", category: "finance", shared: false };
  s = addParam(s); s = patchParam(s, 0, { label: "Countries", type: "list", itemName: "country", max: "8", default: "MX, CO" });
  s = addParam(s); s = patchParam(s, 1, { label: "Waves", type: "number", min: "1", max: "5", default: "2" });
  s = patchWorkflow(s, 0, 0, { title: "Per {country}", repeat: ["countries"] });
  s = patchTask(s, 0, 0, 0, { title: "Plan {country}", agent: "analyst", acceptance: "a\n\n b " });
  s = addTask(s, 0, 0, { title: "Next", agent: "sales" });
  const [t1, t2] = s.phases[0].workflows[0].tasks.map((t) => t.key);
  s = patchTask(s, 0, 0, 1, { deps: [encodeDep(t1), encodeDep(t2, "countries")] });
  s = patchTask(s, 0, 0, 0, { deps: [encodeDep(t1, "countries")] });
  s = { ...s, phases: [{ ...s.phases[0], title: "Phase" }] };
  return s;
}

test("builder state -> TemplateInput", () => {
  const input = toInput(sample(), { baseVersion: 3, note: " fix " });
  assert.equal(input.name, "Budget");
  assert.equal(input.shared, false);
  assert.equal(input.base_version, 3);
  assert.equal(input.note, "fix");
  assert.deepEqual(input.params[0], { key: "countries", label: "Countries", type: "list", default: "MX, CO", item_name: "country", max: 8 });
  assert.deepEqual(input.params[1], { key: "waves", label: "Waves", type: "number", default: "2", min: 1, max: 5 });
  const wf = input.objectives[0].workflows[0];
  assert.deepEqual(wf.repeat, ["countries"]);
  assert.deepEqual(wf.nodes[0].acceptance, ["a", "b"]);
  assert.deepEqual(wf.nodes[1].deps, ["t1", "t2^countries"]);
  assert.equal(wf.nodes[1].approval, undefined);
  assert.ok(!("repeat" in { ...input.objectives[0] }));
});

test("TemplateInput -> builder state round trip", () => {
  const s = sample();
  const back = fromTemplate(toInput(s), (k) => `T(${k})`);
  assert.deepEqual(toInput(back), toInput(s));
  assert.equal(back.params[0].keyEdited, true);
});

test("built-in templates load in the builder with resolved texts", () => {
  const ab = GIANT_TEMPLATES.find((t) => t.key === "annual_budget");
  const s = fromTemplate(ab, (k) => tt(k));
  assert.equal(s.name, "Annual budget of a multinational");
  assert.equal(s.params[0].label, "Business units");
  assert.equal(s.params[0].default, "Retail, Services, Industrial");
  assert.equal(s.params[0].itemName, "unit");
  const input = toInput(s);
  assert.ok(input.objectives[1].workflows[0].repeat.length === 2);
  assert.deepEqual(validateLike({ ...input, params: input.params }, []).filter((i) => i.code !== "no_agent"), []);
});

test("issue parsing", () => {
  assert.deepEqual(issuesFromError("invalid template: unknown_agent t3, cycle"), [{ severity: "error", code: "unknown_agent", node_id: "t3" }, { severity: "error", code: "cycle" }]);
  assert.deepEqual(issuesFromError("something else"), []);
  assert.deepEqual(limitFromDetail("limit_exceeded: max_list_items (limit 12): parameter"), { code: "max_list_items", limit: 12 });
  assert.equal(limitFromDetail("nope"), null);
});

test("local issues", () => {
  const s = blankState();
  const codes = localIssues(s).map((i) => i.code);
  assert.ok(codes.includes("invalid_name") && codes.includes("empty_title") && codes.includes("no_agent"));
});

const L = (key, item) => ({ key, default: "A,B,C", type: "list", item_name: item, max: 5 });
const node = (key, deps = [], extra = {}) => ({ key, title: key, agent: "x", deps, ...extra });

test("expander: repeat, fan-in, same item and previous item", () => {
  const tpl = {
    params: [L("units", "unit"), L("waves", "wave")],
    objectives: [{ key: "o1", title: "o", workflows: [
      { key: "w0", title: "base", nodes: [node("base")] },
      { key: "w1", title: "{unit}", repeat: ["units"], nodes: [node("a", ["base"]), node("b", ["a"])] },
      { key: "w2", title: "all", nodes: [node("sum", ["b"], { kind: "gate" })] },
      { key: "w3", title: "{wave}/{unit}", repeat: ["waves", "units"], nodes: [node("x", ["a", "y^waves"], { title: "{wave}/{unit}" })] },
      { key: "w4", title: "{wave}", repeat: ["waves"], nodes: [node("y", ["x"])] },
    ] }],
  };
  const r = expandTemplate(tpl, { waves: "P, Q" });
  const flat = r.expanded.objectives[0].workflows;
  assert.equal(flat.length, 1 + 3 + 1 + 6 + 2);
  const all = flat.flatMap((w) => w.nodes);
  assert.equal(all.length, 1 + 6 + 1 + 6 + 2);
  const get = (k) => all.find((n) => n.key === k);
  assert.deepEqual(get("a_2").deps, ["base"]);
  assert.deepEqual(get("b_2").deps, ["a_2"]);
  assert.deepEqual(get("sum").deps, ["b_1", "b_2", "b_3"]);       // fan-in
  // w3 instance order: waves x units -> x_1 = P/A, x_4 = Q/A
  assert.deepEqual(get("x_1").deps, ["a_1"]);                       // same unit item; no previous wave for the first one
  assert.deepEqual(get("x_5").deps, ["a_2", "y_1"]);                // second wave: previous wave's y; same unit
  assert.deepEqual(get("y_2").deps, ["x_4", "x_5", "x_6"]);         // y of wave Q depends on all units of its wave
  assert.equal(get("x_5").title, "Q/B");
  const s = summarize("t", r.expanded);
  assert.equal(s.tasks, 16);
  assert.equal(s.gates, 1);
  assert.ok(s.levels >= 6);
  assert.ok(s.parallel >= 3);
  assert.deepEqual(s.agents, ["x"]);
});

test("expander: limits and clamping", () => {
  const tpl = { params: [L("units", "unit"), { key: "n", default: "3", type: "number", min: 1, max: 5 }], objectives: [{ key: "o", title: "o", workflows: [{ key: "w", title: "w", repeat: ["units"], nodes: [node("a")] }] }] };
  assert.equal(expandTemplate(tpl, { units: "1,2,3,4,5,6" }).error.code, "max_list_items");
  assert.equal(expandTemplate(tpl, { units: "1,2,3,4,5,6" }).error.limit, 5);
  assert.equal(expandTemplate(tpl, { units: " , " }).error.code, "invalid_param");
  assert.equal(expandTemplate(tpl, { n: "99" }).expanded.values.n, "5");
  assert.equal(expandTemplate(tpl, { n: "0" }).expanded.values.n, "1");
  assert.equal(expandTemplate({ ...tpl, params: [{ ...L("units", "unit"), max: 50 }] }, { units: Array.from({ length: 50 }, (_, i) => i).join(",") }, (k) => k, 10).error.code, "max_nodes_per_project");
});

test("giant built-in templates: counts match the backend (default and max parameters)", () => {
  assert.equal(GIANT_TEMPLATES.length, 6);
  for (const g of GIANT_TEMPLATES) {
    const st = statsOf(g, tt);
    assert.deepEqual(st, g.stats, g.key);
  }
  assert.equal(GIANT_TEMPLATES.find((g) => g.key === "annual_budget").stats.tasks, 85);
  assert.equal(GIANT_TEMPLATES.find((g) => g.key === "erp_migration").stats.gates, 4);
});

test("validateLike finds the structural problems", () => {
  const tpl = { name: "x", params: [], objectives: [{ key: "o1", title: "o", workflows: [{ key: "w1", title: "w", nodes: [
    node("a", ["b"]), node("b", ["a"]), node("c", ["zz", "c"]), { key: "d", title: "", agent: "" }, node("a"),
  ] }] }] };
  const codes = new Set(validateLike(tpl, ["x"]).map((i) => i.code));
  for (const c of ["cycle", "unknown_dependency", "self_dependency", "empty_title", "no_agent", "duplicate_key"]) assert.ok(codes.has(c), c);
});

test("every giant template validates and duplicates without issues (previous-item chains are not cycles)", () => {
  for (const t of GIANT_TEMPLATES) {
    const input = toInput(fromTemplate(t, (k) => k));
    assert.deepEqual(validateLike({ ...input, name: input.name || "x" }, []), [], t.key);
  }
});
