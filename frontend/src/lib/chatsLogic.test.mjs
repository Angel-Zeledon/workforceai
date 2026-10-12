// Unit tests of the chat history logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  agentBadges, convForThread, dayKey, highlightParts, matches, mergeById, pastConversations, preview, threadFromPath, threadOf, threadPath, turnAgents,
} from "./chatsLogic.ts";

let n = 0;
const msg = (id, conversation, from, to, turn_id, text = id) => ({ id, conversation, from, to, turn_id, text, ts: new Date(Date.UTC(2026, 2, 1, 9, 0, ++n)).toISOString() });

// Same scenario as the backend suite (backend/internal/application/chathistorytest).
const all = [
  msg("u1", "office", "user", "all", "T1"),
  msg("a1", "office", "assistant", "user", "T1"),
  msg("c1", "office", "accounting", "user", "T1"),
  msg("u2", "office", "user", "all", "T2"),
  msg("c2", "office", "accounting", "user", "T2"),
  msg("u3", "agent:sales", "user", "sales", "T3"),
  msg("s3", "agent:sales", "sales", "user", "T3"),
  msg("u4", "office", "user", "all", "T4"),
  msg("a4", "office", "assistant", "user", "T4"),
  msg("s4", "office", "sales", "user", "T4"),
  msg("d1", "rq1", "assistant", "sales", null),
];
const ids = (xs) => xs.map((m) => m.id);

test("General is every office and 1:1 message, chronologically, without request conversations", () => {
  assert.deepEqual(ids(threadOf(all, "general")), ["u1", "a1", "c1", "u2", "c2", "u3", "s3", "u4", "a4", "s4"]);
});

test("an agent thread holds the user messages routed to it, as the same messages", () => {
  const acc = threadOf(all, "accounting");
  assert.deepEqual(ids(acc), ["u1", "c1", "u2", "c2"]);
  assert.deepEqual(ids(threadOf(all, "sales")), ["u3", "s3", "u4", "s4"]);
  assert.deepEqual(threadOf(all, "hr"), []);
  const gen = new Map(threadOf(all, "general").map((m) => [m.id, m]));
  for (const m of acc) assert.equal(gen.get(m.id), m, "same object, not a copy");
});

test("a 1:1 message shows in General and a later one is in both views", () => {
  const more = [...all, msg("u5", "agent:accounting", "user", "accounting", "T5")];
  assert.equal(threadOf(more, "general").at(-1).id, "u5");
  assert.equal(threadOf(more, "accounting").at(-1).id, "u5");
});

test("badges say where a message was routed", () => {
  const turns = turnAgents(all);
  const by = Object.fromEntries(all.map((m) => [m.id, agentBadges(m, turns)]));
  assert.deepEqual(by.u1, ["assistant", "accounting"]);
  assert.deepEqual(by.u2, ["accounting"]);
  assert.deepEqual(by.u3, ["sales"]);
  assert.deepEqual(by.c1, ["accounting", "assistant"]);
  assert.deepEqual(agentBadges({ ...all[0], agent_ids: ["hr"] }, new Map()), ["hr"]);
});

test("mergeById keeps the newest data and drops the local echo once the real message arrived", () => {
  const local = { ...msg("local:x", "office", "user", "all", null, "hola"), ts: "2026-03-01T09:00:00.000Z" };
  const real = { ...msg("u9", "office", "user", "all", "T9", "hola"), ts: "2026-03-01T09:00:01.000Z" };
  assert.deepEqual(ids(mergeById([local])), ["local:x"]);
  assert.deepEqual(ids(mergeById([local], [real])), ["u9"]);
  const merged = mergeById([{ ...real, agent_ids: [] }], [{ ...real, agent_ids: ["sales"] }]);
  assert.equal(merged.length, 1);
  assert.deepEqual(merged[0].agent_ids, ["sales"]);
});

test("past conversations are the request conversations, newest first, archived apart", () => {
  const rows = [
    { id: "office", title: "Oficina", kind: "office", archived: false, updated_at: "2026-03-05T00:00:00Z", request_id: null },
    { id: "r1", title: "A", kind: "request", archived: false, updated_at: "2026-03-01T00:00:00Z", request_id: "q1" },
    { id: "r2", title: "B", kind: "request", archived: false, updated_at: "2026-03-03T00:00:00Z", request_id: "q2" },
    { id: "r3", title: "C", kind: "request", archived: true, updated_at: "2026-03-04T00:00:00Z", request_id: "q3" },
  ];
  assert.deepEqual(pastConversations(rows, false).map((r) => r.id), ["r2", "r1"]);
  assert.deepEqual(pastConversations(rows, true).map((r) => r.id), ["r3"]);
});

test("search helpers are literal and case-insensitive", () => {
  assert.equal(matches("Pipeline sano", "PIPE"), true);
  assert.equal(matches("100% listo", "%"), true);
  assert.equal(matches("hola", "_"), false);
  assert.deepEqual(highlightParts("El margen es 31%", "MARGEN"), [{ text: "El ", hit: false }, { text: "margen", hit: true }, { text: " es 31%", hit: false }]);
  assert.deepEqual(highlightParts("abc", ""), [{ text: "abc", hit: false }]);
  assert.deepEqual(highlightParts("a.b", "."), [{ text: "a", hit: false }, { text: ".", hit: true }, { text: "b", hit: false }]);
  assert.equal(preview("  a   b\n c "), "a b c");
  assert.equal(preview("x".repeat(200), 10).length, 10);
});

test("thread paths round-trip", () => {
  assert.equal(threadPath("general"), "/chats");
  assert.equal(threadPath("sales"), "/chats/sales");
  assert.equal(threadFromPath("/chats"), "general");
  assert.equal(threadFromPath("/chats/"), "general");
  assert.equal(threadFromPath("/chats/sales"), "sales");
  assert.equal(threadFromPath("/chats/a%20b"), "a b");
  assert.equal(convForThread("general"), "office");
  assert.equal(convForThread("sales"), "agent:sales");
  assert.equal(dayKey("2026-03-01T12:00:00"), "2026-03-01");
});
