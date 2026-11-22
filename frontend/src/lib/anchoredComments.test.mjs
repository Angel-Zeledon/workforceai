// Unit tests of the anchored-comments helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { ANCHORABLE, anchorState, buildThreads, canAskChange, cleanQuote, findQuote, openCount, shortQuote } from "./anchoredComments.ts";

const c = (id, over = {}) => ({ id, file_id: "f1", author: { kind: "person", id: "u1" }, body: "x", resolved: false, created_at: `2026-11-21T10:00:0${id.length % 10}Z`, ...over });

test("cleanQuote trims, normalizes breaks and refuses empty or huge selections", () => {
  assert.equal(cleanQuote("  Pago a   60 días.\r\n\r\n\r\n\r\nOtro  "), "Pago a 60 días.\n\nOtro");
  assert.equal(cleanQuote("   \n "), null);
  assert.equal(cleanQuote("a".repeat(2001)), null);
  assert.equal(cleanQuote("a".repeat(2000))?.length, 2000);
});

test("buildThreads groups replies under their root, open threads first", () => {
  const a = c("a", { created_at: "2026-11-21T10:00:00Z", resolved: true });
  const b = c("b", { created_at: "2026-11-21T10:01:00Z" });
  const r2 = c("r2", { parent_id: "b", created_at: "2026-11-21T10:03:00Z" });
  const r1 = c("r1", { parent_id: "b", created_at: "2026-11-21T10:02:00Z" });
  const th = buildThreads([a, r2, b, r1]);
  assert.deepEqual(th.map((t) => t.root.id), ["b", "a"]);
  assert.deepEqual(th[0].replies.map((r) => r.id), ["r1", "r2"]);
  assert.equal(openCount([a, b, r1, r2]), 1);
});

test("anchorState tells a found passage, a rewritten one and a note on the whole file", () => {
  const anchor = { quote: "q", prefix: "", suffix: "", version: 1, start: 0, end: 1 };
  assert.equal(anchorState(c("n")), "general");
  assert.equal(anchorState(c("f", { anchor, location: { status: "moved", start: 4, end: 5 } })), "found");
  assert.equal(anchorState(c("l", { anchor, location: { status: "lost", start: -1, end: -1 } })), "changed");
  assert.equal(anchorState(c("u", { anchor })), "found");
});

test("canAskChange only for an open passage that exists and was not asked yet", () => {
  const anchor = { quote: "q", prefix: "", suffix: "", version: 1, start: 0, end: 1 };
  const ok = c("o", { anchor, location: { status: "intact", start: 0, end: 1 } });
  assert.equal(canAskChange(ok), true);
  assert.equal(canAskChange({ ...ok, resolved: true }), false);
  assert.equal(canAskChange({ ...ok, change: { agent_id: "legal", base_version: 1, requested_by: "person:u1", requested_at: "x" } }), false);
  assert.equal(canAskChange({ ...ok, location: { status: "lost", start: -1, end: -1 } }), false);
  assert.equal(canAskChange({ ...ok, anchor: null }), false);
  assert.equal(canAskChange({ ...ok, parent_id: "p" }), false);
});

test("findQuote finds a passage across text nodes, ignoring whitespace and line breaks", () => {
  const nodes = ["Título", "El pago se hará", " a 60 días ", "de la factura.", "Garantía"];
  const r = findQuote(nodes, "El pago se hará a 60 días\n\nde la factura.");
  assert.deepEqual(r, { startNode: 1, startOffset: 0, endNode: 3, endOffset: 14 });
  assert.equal(findQuote(nodes, "no existe"), null);
  assert.equal(findQuote(nodes, "   "), null);
});

test("findQuote uses prefix and suffix to pick the right repeat", () => {
  const nodes = ["Primero: Pagar a 30 días.", "Segundo: Pagar a 30 días.", "Tercero: nada"];
  const second = findQuote(nodes, "Pagar a 30 días.", "Segundo:", "Tercero: nada");
  assert.equal(second?.startNode, 1);
  assert.equal(second?.startOffset, 9);
  const first = findQuote(nodes, "Pagar a 30 días.", "Primero:", "Segundo:");
  assert.equal(first?.startNode, 0);
});

test("shortQuote shortens long quotes with an ellipsis and ANCHORABLE starts with text documents", () => {
  assert.equal(shortQuote("a  b\n c"), "a b c");
  assert.equal([...shortQuote("x".repeat(300), 20)].length, 20);
  assert.ok(shortQuote("x".repeat(300), 20).endsWith("…"));
  assert.deepEqual(ANCHORABLE, ["markdown", "text"]);
});
