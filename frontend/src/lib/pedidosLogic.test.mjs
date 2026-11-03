// Unit tests of the "pedido" logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  foldText, needsAttention, pedidoAgents, pedidoCardAnchors, pedidoFromSearch, pedidoHref, pedidoRows, pedidoStatus, searchGroups,
} from "./pedidosLogic.ts";

test("pedidoStatus: no request loaded is unknown, never a crash", () => {
  assert.equal(pedidoStatus(undefined, [], 0, 0), "unknown");
});

test("pedidoStatus: failed and done win over everything else", () => {
  assert.equal(pedidoStatus({ status: "failed" }, [{ status: "needs_input" }], 2, 1), "failed");
  assert.equal(pedidoStatus({ status: "done" }, [{ status: "awaiting_approval" }], 1, 1), "done");
});

test("pedidoStatus: needs your help comes from an open question or a needs_input task", () => {
  assert.equal(pedidoStatus({ status: "running" }, [{ status: "running" }], 1, 0), "needs_help");
  assert.equal(pedidoStatus({ status: "running" }, [{ status: "needs_input" }], 0, 0), "needs_help");
  // help is asked before an approval: the person has to answer first
  assert.equal(pedidoStatus({ status: "awaiting_approval" }, [{ status: "needs_input" }], 0, 1), "needs_help");
});

test("pedidoStatus: approvals, confirmation and pause", () => {
  assert.equal(pedidoStatus({ status: "awaiting_approval" }, [], 0, 0), "needs_approval");
  assert.equal(pedidoStatus({ status: "awaiting_confirmation" }, [], 0, 0), "needs_approval");
  assert.equal(pedidoStatus({ status: "running" }, [{ status: "awaiting_approval" }], 0, 0), "needs_approval");
  assert.equal(pedidoStatus({ status: "running" }, [], 0, 1), "needs_approval");
  assert.equal(pedidoStatus({ status: "paused" }, [], 0, 0), "paused");
  assert.equal(pedidoStatus({ status: "planning" }, [], 0, 0), "running");
  assert.equal(pedidoStatus({ status: "running" }, [{ status: "done" }, { status: "pending" }], 0, 0), "running");
});

test("needsAttention", () => {
  assert.equal(needsAttention("needs_help"), true);
  assert.equal(needsAttention("needs_approval"), true);
  assert.equal(needsAttention("running"), false);
  assert.equal(needsAttention("done"), false);
});

test("pedidoAgents: task agents in order, else the participants without the user", () => {
  assert.deepEqual(pedidoAgents([{ agent_id: "analyst" }, { agent_id: "finance" }, { agent_id: "analyst" }], ["user", "assistant"]), ["analyst", "finance"]);
  assert.deepEqual(pedidoAgents([], ["user", "assistant", "system"]), ["assistant"]);
});

const conv = (id, request_id, updated_at, extra = {}) => ({
  id, title: `T ${id}`, kind: "request", request_id, project_id: null, archived: false, unread: 0, updated_at, participants: ["user", "assistant"], ...extra,
});
const ctx = {
  requests: { r1: { status: "done" }, r2: { status: "running" }, r3: { status: "running" } },
  tasks: [{ request_id: "r2", agent_id: "finance", status: "running" }, { request_id: "r3", agent_id: "analyst", status: "running" }],
  questions: [{ request_id: "r3", status: "open" }, { request_id: "r1", status: "answered" }],
  approvals: [],
};

test("pedidoRows: only request conversations, the ones needing you first, then newest", () => {
  const convs = [
    conv("c1", "r1", "2026-11-03T10:00:00Z"),
    conv("c2", "r2", "2026-11-03T12:00:00Z"),
    conv("c3", "r3", "2026-11-03T08:00:00Z"),
    { ...conv("office", null, "2026-11-03T13:00:00Z"), kind: "office" },
    conv("c4", "r4", "2026-11-03T09:00:00Z", { archived: true }),
  ];
  const rows = pedidoRows(convs, ctx, { archived: false });
  assert.deepEqual(rows.map((r) => r.id), ["r3", "r2", "r1"]);
  assert.equal(rows[0].status, "needs_help");
  assert.deepEqual(rows[0].agents, ["analyst"]);
  assert.equal(rows[0].convId, "c3");
  assert.equal(rows[2].status, "done");
  assert.deepEqual(rows[2].agents, ["assistant"]);
  // archived ones are listed only with "see archived", and an unknown request is "unknown"
  const arch = pedidoRows(convs, ctx, { archived: true });
  assert.deepEqual(arch.map((r) => [r.id, r.status]), [["r4", "unknown"]]);
});

test("pedidoCardAnchors: one card per request, on its latest message", () => {
  const msgs = [
    { id: "m1", request_id: "r1" }, { id: "m2" }, { id: "m3", request_id: "r2" }, { id: "m4", request_id: "r1" }, { id: "m5", request_id: null },
  ];
  assert.deepEqual([...pedidoCardAnchors(msgs)].sort(), ["m3", "m4"]);
});

test("searchGroups: three groups, accent and case insensitive, backend hits mapped to their group", () => {
  const agents = [{ key: "general", name: "Toda la oficina", sub: "" }, { key: "finance", name: "Lucía Paz", sub: "Contadora" }, { key: "analyst", name: "Diego", sub: "Analista" }];
  const people = [{ id: "p1", title: "Ana Gómez", sub: "" }, { id: "p2", title: "#ventas", sub: "Conciliación de marzo" }];
  const pedidos = [{ id: "r1", convId: "c1", title: "Informe de conciliación" }, { id: "r2", convId: "c2", title: "Precios" }];
  const hits = [
    { id: "agent:analyst", kind: "agent", agent_id: "analyst", request_id: null },
    { id: "c2", kind: "request", request_id: "r2" },
    { id: "office", kind: "office", request_id: null },
  ];
  const g = searchGroups("conciliacion", { agents, people, pedidos, hits });
  assert.deepEqual(g.agents, ["analyst", "general"]);
  assert.deepEqual(g.people, ["p2"]);
  assert.deepEqual(g.pedidos, ["r1", "r2"]);
  assert.deepEqual(searchGroups("LUCIA", { agents, people, pedidos, hits: [] }).agents, ["finance"]);
  assert.deepEqual(searchGroups("contadora", { agents, people, pedidos, hits: [] }).agents, ["finance"]);
  assert.deepEqual(searchGroups("  ", { agents, people, pedidos, hits }), { agents: [], people: [], pedidos: [] });
});

test("foldText drops accents and case", () => {
  assert.equal(foldText("  Infórme ÁRBOL "), "informe arbol");
});

test("pedidoFromSearch: the pedido param, and the old conversation link", () => {
  assert.deepEqual(pedidoFromSearch("?pedido=r1"), { pedido: "r1", conv: "" });
  assert.deepEqual(pedidoFromSearch("conv=c9&x=1"), { pedido: "", conv: "c9" });
  assert.deepEqual(pedidoFromSearch(""), { pedido: "", conv: "" });
});

test("pedidoHref: sets or removes the pedido, drops the old conv link, keeps the rest", () => {
  assert.equal(pedidoHref("/chats", "", "r1"), "/chats?pedido=r1");
  assert.equal(pedidoHref("/chats/finance", "?x=1&pedido=old", "r 2"), "/chats/finance?x=1&pedido=r+2");
  assert.equal(pedidoHref("/chats/finance", "?pedido=r1&x=1", null), "/chats/finance?x=1");
  assert.equal(pedidoHref("/chats", "?conv=c9", null), "/chats");
  assert.equal(pedidoHref("/chats", "?conv=c9", "r9"), "/chats?pedido=r9");
});
