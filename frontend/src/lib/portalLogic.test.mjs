// Unit tests of the client portal helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { linkStatus, portalUrl, publicProjectStatus, publicTaskStatus, safeAccent, textOn, toPublicView, tokenFromHash, unexpectedKeys } from "./portalLogic.ts";

const node = (over) => ({
  id: "node-SECRET", project_id: "proj-SECRET", objective_id: "o1", parent_id: null, kind: "task", title: "Conciliar bancos", agent_id: "agent-SECRET",
  task_id: "task-SECRET", state: "done", attempt: 1, max_attempts: 3, progress: 100, depends_on: [], delegation_depth: 0, delegation_chain: ["agent-SECRET"],
  dag_level: 0, wbs_path: "1.1", complexity: "M", est_cost_usd: 56.78, est_seconds: 10, cost_usd: 12.34, plan_start_ms: null, plan_end_ms: null,
  started_at: "2026-10-20T09:00:00Z", finished_at: "2026-10-20T10:00:00Z", rev: 1, ...over,
});

const detail = () => ({
  project: { id: "proj-SECRET", name: "Cierre mensual", goal: "SECRET-GOAL prompt", status: "waiting_human", control: "active", template_id: "tpl-SECRET",
    budget_usd: 4321, spent_usd: 987, tasks_done: 1, tasks_total: 3, running: 0, awaiting: 1, failed: 1, light: "amber",
    created_at: "2026-10-18T00:00:00Z", started_at: "2026-10-18T01:00:00Z", finished_at: null, deadline_at: null, objectives_count: 1 },
  objectives: [{ id: "o1", title: "Conciliaciones", position: 0 }],
  nodes: [
    node({}),
    node({ id: "n2", title: "Pagar proveedores", state: "awaiting_approval", finished_at: null }),
    node({ id: "n3", title: "Estado de resultados", state: "failed", finished_at: null }),
    node({ id: "n4", kind: "gate", title: "GATE-SECRET", state: "awaiting_approval" }),
    node({ id: "n5", kind: "wait", title: "WAIT-SECRET", state: "pending" }),
    node({ id: "n6", title: "SUPERSEDED-SECRET", state: "cancelled", superseded: "replaced" }),
  ],
  approvals: [{ id: "appr-SECRET", project_id: "proj-SECRET", node_id: "n2", agent_id: "agent-SECRET", action: "pay", title: "APPROVAL-SECRET", details: "x", risk: "high", status: "pending", created_at: "", resolved_at: null }],
  budget: { mode: "hard", warn_at: [], on_hard: "pause", reserve_pct: 0 }, estimate: null, structure_version: 1, max_parallel: 2, planning: null,
});

test("toPublicView is an allowlist: no forbidden key or value", () => {
  const v = toPublicView(detail(), { firmName: "Despacho Pérez", accent: "#0b5fff", hasLogo: true, deliverables: [{ title: "Informe", kind: "doc", updated_at: "2026-10-20T11:00:00Z" }], now: new Date("2026-10-21T00:00:00Z") });
  assert.deepEqual(unexpectedKeys(v), []);
  const doc = JSON.stringify(v).toLowerCase();
  for (const bad of ["secret", "4321", "987", "12.34", "56.78", "agent", "approval", "cost", "budget", "goal", "high"]) {
    assert.ok(!doc.includes(bad), `public view contains ${bad}: ${doc}`);
  }
  assert.equal(v.project.title, "Cierre mensual");
  assert.equal(v.project.status, "in_progress");
  assert.deepEqual([v.project.tasks_done, v.project.tasks_total, v.project.progress_pct], [1, 3, 33]);
  assert.deepEqual(v.milestones[0].tasks.map((t) => t.status), ["done", "in_progress", "pending"]);
  assert.equal(v.updated_at, "2026-10-20T11:00:00Z");
});

test("a custom title replaces the project name and its key", () => {
  const d = detail(); d.project.name_key = "proj.x";
  const v = toPublicView(d, { firmName: "", accent: "", hasLogo: false, title: "Cierre de octubre" });
  assert.equal(v.project.title, "Cierre de octubre");
  assert.equal(v.project.title_key, undefined);
});

test("unexpectedKeys catches a leak", () => {
  assert.deepEqual(unexpectedKeys({ project: { title: "x", cost_usd: 1 }, milestones: [{ tasks: [{ agent_id: "a" }] }] }), ["cost_usd", "agent_id"]);
  assert.deepEqual(unexpectedKeys({ title_params: { goal: "free text keys are not field names" } }), []);
});

test("statuses are coarse", () => {
  assert.equal(publicProjectStatus("failed"), "on_hold");
  assert.equal(publicProjectStatus("draft"), "planning");
  assert.equal(publicProjectStatus("done"), "completed");
  assert.equal(publicProjectStatus("cancelled"), "closed");
  assert.equal(publicTaskStatus("awaiting_approval"), "in_progress");
  assert.equal(publicTaskStatus("blocked"), "pending");
  assert.equal(publicTaskStatus("skipped"), "done");
});

test("token from the fragment, URL and colors", () => {
  assert.equal(tokenFromHash("#abc_DEF-123"), "abc_DEF-123");
  assert.equal(tokenFromHash("#t=abc"), "abc");
  assert.equal(tokenFromHash("#<script>"), "");
  assert.equal(tokenFromHash(""), "");
  assert.equal(portalUrl("https://x.example/", "tok"), "https://x.example/portal#tok");
  assert.equal(safeAccent("#0B5FFF"), "#0b5fff");
  assert.equal(safeAccent("red;background:url(x)"), "#0b1f3a");
  assert.equal(textOn("#ffffff"), "#000000");
  assert.equal(textOn("#0b1f3a"), "#ffffff");
});

test("link status", () => {
  const now = Date.parse("2026-10-21T00:00:00Z");
  assert.equal(linkStatus({ revoked_at: "2026-10-20T00:00:00Z", expires_at: null }, now), "revoked");
  assert.equal(linkStatus({ revoked_at: null, expires_at: "2026-10-20T00:00:00Z" }, now), "expired");
  assert.equal(linkStatus({ revoked_at: null, expires_at: null }, now), "active");
});
