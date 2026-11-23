// Unit tests of the list view as a workspace (grouping, targets, what needs you / is running / is ready). Run: npm run test:unit.
import assert from "node:assert/strict";
import { test } from "node:test";
import { convOf, exampleRoles, groupAgents, inProgress, needsYouAgentIds, needsYouItems, railGroupOf, readyItems, stepKind, targetOf, validTarget } from "./workspaceLogic.ts";
import { isListShown, resolveView } from "../../../lib/officeViewLogic.ts";

const A = (id, state, extra = {}) => ({ id, name: id.toUpperCase(), role: "sales", state, activity: "", ...extra });

test("an agent waits for the person when something is theirs or it is stuck; working states are working; the rest is free", () => {
  assert.equal(railGroupOf({ state: "idle" }, true), "waiting");
  assert.equal(railGroupOf({ state: "awaiting_approval" }, false), "waiting");
  assert.equal(railGroupOf({ state: "working" }, false), "working");
  assert.equal(railGroupOf({ state: "thinking" }, false), "working");
  assert.equal(railGroupOf({ state: "idle" }, false), "free");
  assert.equal(railGroupOf({ state: "completed" }, false), "free");
});

test("groups come in the order Te esperan / Trabajando / Disponibles and empty groups do not appear", () => {
  const agents = { a: A("a", "idle"), b: A("b", "working"), c: A("c", "idle"), d: A("d", "idle") };
  const g = groupAgents(["a", "b", "c", "d"], agents, new Set(["c"]));
  assert.deepEqual(g, [{ group: "waiting", ids: ["c"] }, { group: "working", ids: ["b"] }, { group: "free", ids: ["a", "d"] }]);
  assert.deepEqual(groupAgents(["a"], { a: agents.a }, new Set()), [{ group: "free", ids: ["a"] }]);
  assert.deepEqual(groupAgents([], {}, new Set()), []);
});

test("the search folds accents and case and looks at the name, role and activity", () => {
  const agents = { a: A("a", "idle", { name: "Sofía", role: "sales" }), b: A("b", "working", { name: "Iván", activity: "Revisa el contrato" }) };
  assert.deepEqual(groupAgents(["a", "b"], agents, new Set(), "sofia").flatMap((s) => s.ids), ["a"]);
  assert.deepEqual(groupAgents(["a", "b"], agents, new Set(), "CONTRATO").flatMap((s) => s.ids), ["b"]);
  assert.deepEqual(groupAgents(["a", "b"], agents, new Set(), "zzz"), []);
});

test("an agent that is not in the store is skipped", () => {
  assert.deepEqual(groupAgents(["ghost", "a"], { a: A("a", "idle") }, new Set()), [{ group: "free", ids: ["a"] }]);
});

test("the conversation of a target is the office or agent:<id>, and back", () => {
  assert.equal(convOf(null), "office");
  assert.equal(convOf("all"), "office");
  assert.equal(convOf("ana"), "agent:ana");
  assert.equal(targetOf("office"), null);
  assert.equal(targetOf("agent:ana"), "ana");
  assert.equal(validTarget("ana", { ana: {} }), "ana");
  assert.equal(validTarget("gone", { ana: {} }), null);
});

test("needs-you: pending approvals and open questions, the oldest first; decided ones do not count", () => {
  const items = needsYouItems(
    [{ id: "a1", status: "pending", created_at: "2026-01-01T10:05:00Z" }, { id: "a2", status: "approved", created_at: "2026-01-01T09:00:00Z" }],
    [{ id: "q1", status: "open", created_at: "2026-01-01T10:00:00Z" }, { id: "q2", status: "answered", created_at: "2026-01-01T08:00:00Z" }, { id: "q3", status: "open", created_at: "2026-01-01T11:00:00Z", for_me: false }],
  );
  assert.deepEqual(items.map((i) => i.id), ["q1", "a1"]);
  const ids = needsYouAgentIds([{ agent_id: "x", status: "pending" }, { agent_id: "y", status: "rejected" }], [{ agent_id: "z", status: "open" }]);
  assert.deepEqual([...ids].sort(), ["x", "z"]);
});

test("steps read as done / active / waiting / queued / failed", () => {
  assert.equal(stepKind("done"), "done");
  assert.equal(stepKind("running"), "active");
  assert.equal(stepKind("awaiting_approval"), "waiting");
  assert.equal(stepKind("needs_input"), "waiting");
  assert.equal(stepKind("pending"), "queued");
  assert.equal(stepKind("failed"), "failed");
});

test("in progress: open requests with their steps and progress, newest first; finished ones leave", () => {
  const requests = {
    r1: { id: "r1", text: "Informe", status: "running", created_at: "2026-01-01T10:00:00Z" },
    r2: { id: "r2", text: "Viejo", status: "done", created_at: "2026-01-01T09:00:00Z" },
    r3: { id: "r3", text: "Nuevo", status: "planning", created_at: "2026-01-01T11:00:00Z" },
  };
  const tasks = [
    { id: "t1", request_id: "r1", agent_id: "a", status: "done", title: "Buscar", created_at: "2026-01-01T10:01:00Z" },
    { id: "t2", request_id: "r1", agent_id: "b", status: "running", title: "Redactar", created_at: "2026-01-01T10:02:00Z" },
    { id: "t3", request_id: "r1", agent_id: "b", status: "pending", title: "Revisar", created_at: "2026-01-01T10:03:00Z" },
  ];
  const rows = inProgress(requests, tasks);
  assert.deepEqual(rows.map((r) => r.id), ["r3", "r1"]);
  const r1 = rows[1];
  assert.deepEqual(r1.steps.map((s) => s.kind), ["done", "active", "queued"]);
  assert.equal(r1.pct, 33);
  assert.deepEqual(r1.agents, ["a", "b"]);
  assert.equal(rows[0].total, 0);
  assert.equal(rows[0].pct, 0);
  assert.deepEqual(inProgress(requests, tasks, { agent: "a" }).map((r) => r.id), ["r1"]);
});

test("ready: the latest version of each written file plus reports, newest first, attachments excluded", () => {
  const chat = {
    office: [
      { id: "m1", from: "user", ts: "2026-01-01T10:00:00Z", files: [{ path: "/a.pdf", name: "a.pdf", action: "attach" }] },
      { id: "m2", from: "ana", ts: "2026-01-01T10:10:00Z", files: [{ path: "/b.docx", name: "b.docx", action: "write", version: 1 }] },
    ],
    "agent:ana": [{ id: "m3", from: "ana", ts: "2026-01-01T10:20:00Z", files: [{ path: "/b.docx", name: "b.docx", action: "write", version: 2 }] }],
  };
  const reports = { r: { id: "r", title: "Informe", created_at: "2026-01-01T10:15:00Z" } };
  const items = readyItems(chat, reports);
  assert.deepEqual(items.map((i) => i.key), ["f:/b.docx", "r:r"]);
  assert.equal(items[0].file.version, 2);
  assert.equal(readyItems(chat, reports, 1).length, 1);
});

test("examples: one per distinct known role of the real team, at most n", () => {
  const agents = { a: { role: "sales" }, b: { role: "sales" }, c: { role: "legal" }, d: { role: "weird" }, e: { role: "hr" } };
  const known = new Set(["sales", "legal", "hr"]);
  assert.deepEqual(exampleRoles(["a", "b", "c", "d", "e"], agents, known).map((x) => x.agentId), ["a", "c", "e"]);
  assert.equal(exampleRoles(["a", "c", "e"], agents, known, 2).length, 2);
  assert.deepEqual(exampleRoles([], {}, known), []);
});

test("the view: the list unless the 3D is on; with it on, the last used one (the 3D when none is stored)", () => {
  assert.equal(resolveView(false, null), "list");
  assert.equal(resolveView(false, "3d"), "list");
  assert.equal(resolveView(true, null), "3d");
  assert.equal(resolveView(true, "3d"), "3d");
  assert.equal(resolveView(true, "list"), "list");
  assert.equal(isListShown("dashboard", "3d"), true);
  assert.equal(isListShown("office", "list"), true);
  assert.equal(isListShown("office", "3d"), false);
});
