// Unit tests of the quality score UI helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { checksText, deltaText, parseChecks, pctText, trendArrow, weightPct } from "./qualityLogic.ts";

test("pctText rounds to whole percentages and shows a dash without data", () => {
  assert.equal(pctText(0.8333), "83%");
  assert.equal(pctText(1), "100%");
  assert.equal(pctText(0), "0%");
  assert.equal(pctText(null), "–");
  assert.equal(pctText(undefined), "–");
});

test("weightPct and deltaText", () => {
  assert.equal(weightPct(0.3), 30);
  assert.equal(weightPct(0.15), 15);
  assert.equal(deltaText(-6), "6");
  assert.equal(deltaText(12), "12");
  assert.equal(deltaText(null), "");
});

test("trendArrow never suggests a direction without a trend", () => {
  assert.equal(trendArrow("up"), "▲");
  assert.equal(trendArrow("down"), "▼");
  assert.equal(trendArrow("stable"), "■");
  assert.equal(trendArrow("unknown"), "–");
});

test("parseChecks reads one check per line and rejects anything else", () => {
  assert.deepEqual(parseChecks("contains: 1,200\n\nnot_contains: error "), [
    { kind: "contains", value: "1,200" }, { kind: "not_contains", value: "error" },
  ]);
  assert.deepEqual(parseChecks("CONTAINS : Hola\r\n"), [{ kind: "contains", value: "Hola" }]);
  assert.deepEqual(parseChecks(""), []);
  assert.equal(parseChecks("contains: ok\nregex: a+"), null);
  assert.equal(parseChecks("just some text"), null);
  assert.equal(parseChecks("contains:"), null);
});

test("checksText is the inverse of parseChecks", () => {
  const checks = [{ kind: "contains", value: "a: b" }, { kind: "not_contains", value: "x" }];
  assert.deepEqual(parseChecks(checksText(checks)), checks);
});
