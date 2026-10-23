// Unit tests of the AI payroll / return model (run: npm run test:unit). Same cases as backend roi_test.go.
import assert from "node:assert/strict";
import { test } from "node:test";
import { computeRoi, parseRoiForm, roiExclusion } from "./roiLogic.ts";

const to = new Date("2026-10-22T12:00:00Z");
const from = new Date(+to - 30 * 864e5);
const inWin = new Date(+to - 3600e3).toISOString();
const old = new Date(+from - 3600e3).toISOString();
const task = (id, agent_id, status, finished_at, output = {}) => ({ id, agent_id, status, finished_at, output });
const agents = [{ id: "acc", role: "accounting" }, { id: "leg", role: "legal" }, { id: "idle", role: "hr" }];
const tasks = [
  task("ok1", "acc", "done", inWin),
  task("ok2", "acc", "done", inWin, { review: { verdict: "pass", reworks: 0 } }),
  task("rew", "acc", "done", inWin, { review: { verdict: "pass", reworks: 1 } }),
  task("retry", "acc", "done", inWin, { metrics: { attempts: 2 } }),
  task("skip", "acc", "done", inWin, { metrics: { skipped: true } }),
  task("fail", "leg", "failed", inWin, null),
  task("leg1", "leg", "done", inWin, null),
  task("old", "acc", "done", old),
  task("run", "acc", "running", null),
];
const usage = [{ agent_id: "acc", cost_usd: 0.5, ts: inWin }, { agent_id: "acc", cost_usd: 9, ts: old }, { agent_id: "leg", cost_usd: 0.25, ts: inWin }];

test("roiExclusion", () => {
  assert.equal(roiExclusion(tasks[0]), "");
  assert.equal(roiExclusion(tasks[2]), "rework");
  assert.equal(roiExclusion(tasks[3]), "rework");
  assert.equal(roiExclusion(tasks[4]), "skipped");
  assert.equal(roiExclusion(tasks[5]), "failed");
});

test("without a saved model: the 30-minute assumption and no money", () => {
  const rep = computeRoi({ hourly_rate_usd: null, default_minutes: 30, role_minutes: {} }, false, agents, tasks, usage, from, to, 30);
  assert.equal(rep.methodology.kind, "estimate");
  assert.equal(rep.methodology.default_minutes_source, "assumption");
  assert.equal(rep.methodology.rate_source, "unset");
  assert.equal(rep.agents.length, 2);
  const acc = rep.agents[0];
  assert.deepEqual([acc.agent_id, acc.tasks_counted, acc.excluded_rework, acc.excluded_skipped, acc.est_minutes, acc.est_hours, acc.ai_cost_usd], ["acc", 2, 2, 1, 60, 1, 0.5]);
  assert.equal(acc.est_human_cost_usd, null);
  assert.equal(acc.ai_cost_per_est_hour_usd, 0.5);
  assert.deepEqual([rep.org.tasks_counted, rep.org.excluded_failed, rep.org.est_minutes, rep.org.ai_cost_usd, rep.org.est_human_cost_usd], [3, 1, 90, 0.75, null]);
});

test("the org's own model and rate", () => {
  const rep = computeRoi({ hourly_rate_usd: 40, default_minutes: 20, role_minutes: { legal: 90 } }, true, agents, tasks, usage, from, to, 30);
  const by = Object.fromEntries(rep.agents.map((a) => [a.agent_id, a]));
  assert.deepEqual([by.leg.minutes_per_task, by.leg.minutes_source, by.leg.est_minutes, by.leg.est_human_cost_usd], [90, "role", 90, 60]);
  assert.deepEqual([by.acc.minutes_per_task, by.acc.minutes_source, by.acc.est_minutes], [20, "default", 40]);
  assert.equal(rep.org.est_human_cost_usd, 86.67);
  assert.equal(rep.methodology.rate_source, "org");
});

test("parseRoiForm", () => {
  assert.deepEqual(parseRoiForm("", "30", {}), { ok: true, body: { hourly_rate_usd: null, default_minutes: 30, role_minutes: {} } });
  assert.deepEqual(parseRoiForm("25,555", "15", { Legal: "60", hr: "" }), { ok: true, body: { hourly_rate_usd: 25.56, default_minutes: 15, role_minutes: { legal: 60 } } });
  assert.equal(parseRoiForm("-1", "30", {}).ok, false);
  assert.equal(parseRoiForm("", "", {}).ok, false);
  assert.equal(parseRoiForm("", "999", {}).ok, false);
  assert.equal(parseRoiForm("", "30", { legal: "1.5" }).ok, false);
});
