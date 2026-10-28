// Unit tests of the evidence chain helpers (B6; run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { figureOf, findFigures, kindOf, linkFigures, parseNumber, tracedCount, unverifiedOf } from "./evidenceLogic.ts";

test("parseNumber reads both decimal conventions like the backend", () => {
  for (const [s, n] of [["50,000", 50000], ["50.000", 50000], ["1.200.000,50", 1200000.5], ["1,200,000.50", 1200000.5], ["24,5", 24.5], ["0.24", 0.24], ["7", 7]]) {
    assert.equal(parseNumber(s), n, s);
  }
});

test("findFigures keeps money and percent marks and skips ranges and codes", () => {
  const text = "Ticket de $50,000, margen 24 % y entrega en 3-4 semanas (Q3, celda B12).";
  const f = findFigures(text);
  assert.deepEqual(f.map((x) => text.slice(x.start, x.end)), ["$50,000", "24 %", "3", "4"]);
  assert.equal(f[0].money, true);
  assert.equal(f[1].pct, true);
  assert.equal(f[3].n, 4); // "3-4": a range, never -4
  assert.equal(figureOf("USD 7,800").money, true);
});

const v = (value, ref, extra = {}) => ({ value, ref, status: "verified", source: { task_id: "t" }, task_id: "t", agent_id: "a", ...extra });

test("linkFigures links only figures that match a verified citation with a source", () => {
  const cites = [
    v("$50,000", "file:/Clients/brief.md"),
    v("24%", "cell:art!Margen!B7"),
    { value: "28%", ref: "memory:x", status: "unverified", reason: "not_seen", task_id: "t", agent_id: "a" },
    v("31%", "task:t1", { source: null }), // verified but without a source: not openable
  ];
  const segs = linkFigures("La propuesta de $50,000 deja 24% (no 28% ni 31%); 2 personas.", cites);
  const linked = segs.filter((s) => s.cites).map((s) => s.text);
  assert.deepEqual(linked, ["$50,000", "24%"]);
  assert.equal(segs.map((s) => s.text).join(""), "La propuesta de $50,000 deja 24% (no 28% ni 31%); 2 personas.");
});

test("a plain cited number matches its formatted form, but percentages never match bare numbers", () => {
  const segs = linkFigures("Ticket USD 50.000 y 24 clientes", [v("50000", "task:t1"), v("24%", "task:t2")]);
  assert.deepEqual(segs.filter((s) => s.cites).map((s) => s.text), ["USD 50.000"]);
});

test("small bare numbers are not linked even when cited", () => {
  const segs = linkFigures("Contratar 2 personas", [v("2", "task:t5")]);
  assert.equal(segs.length, 1);
  assert.equal(segs[0].cites, undefined);
});

test("text without verified citations comes back untouched", () => {
  assert.deepEqual(linkFigures("Margen 24%", []), [{ text: "Margen 24%" }]);
});

test("helpers: kind, unverified list and traced count", () => {
  assert.equal(kindOf({ value: "1", ref: "conn:email:m1" }), "conn");
  assert.equal(kindOf({ value: "1", ref: "web:x" }), "");
  const cites = [v("24%", "task:a"), v("24 %", "cell:x!S!A1"), v("$50,000", "task:b"), { value: "9", ref: "task:z", status: "unverified" }];
  assert.equal(tracedCount(cites), 2);
  assert.equal(unverifiedOf(cites).length, 1);
});
