// Unit tests of the proactive suggestion helpers and the mock that demos one suggestion per signal (run: npm run test:unit).
import assert from "node:assert/strict";
import fs from "node:fs";
import { test } from "node:test";
import { METRIC_SIGNALS, SIGNAL_FIELDS, clampThreshold, signalText } from "./initiativeLogic.ts";
import { MockInitiatives } from "./mock/initiative-mock.ts";

const spec = { signals: METRIC_SIGNALS, fields: SIGNAL_FIELDS };
const catalog = (l) => JSON.parse(fs.readFileSync(new URL(`./i18n/${l}.json`, import.meta.url), "utf8"));

test("clampThreshold keeps values inside the backend ranges", () => {
  assert.equal(clampThreshold("metric_change", "change_pct", "0"), 1);
  assert.equal(clampThreshold("metric_change", "change_pct", "5000"), 1000);
  assert.equal(clampThreshold("metric_change", "margin_points", "2.5"), 2.5);
  assert.equal(clampThreshold("spend_pace", "min_days", "3.6"), 4);
  assert.equal(clampThreshold("approval_aging", "min_age_hours", "abc"), 1);
  assert.equal(clampThreshold("quality_drop", "drop_points", "-4"), 1);
});

test("signalText picks the right keys and falls back to the stored text without params", () => {
  assert.equal(signalText("task_failed", { a: "1" }), null);
  assert.equal(signalText("metric_change", null), null);
  assert.equal(signalText("metric_change", {}), null);
  const m = signalText("metric_change", { direction: "down", unit: "points", period: "Sep", column: "Margen" });
  assert.equal(m.title.key, "initiative.signal.metric_change.title.down.points");
  assert.equal(m.body.key, "initiative.signal.metric_change.bodyPeriod");
  assert.equal(signalText("metric_change", { direction: "up", unit: "pct" }).body.key, "initiative.signal.metric_change.body");
  assert.equal(signalText("quality_drop", { agent: "x" }).title.key, "initiative.signal.quality_drop.title");
});

test("every key signalText can produce exists in es, en and pt", () => {
  const keys = new Set();
  for (const dir of ["up", "down"]) for (const unit of ["pct", "points"]) for (const period of ["", "p"]) {
    const r = signalText("metric_change", { direction: dir, unit, period });
    keys.add(r.title.key); keys.add(r.body.key);
  }
  for (const s of METRIC_SIGNALS) { const r = signalText(s, { x: "1", direction: "down", unit: "pct" }); keys.add(r.title.key); keys.add(r.body.key); }
  for (const s of METRIC_SIGNALS) { keys.add(`initiative.signals.${s}`); for (const f of SIGNAL_FIELDS[s]) keys.add(`initiative.signals.${s}.${f.field}`); }
  for (const l of ["es", "en", "pt"]) {
    const cat = catalog(l);
    for (const k of keys) assert.ok(cat[k], `${l}: missing ${k}`);
  }
});

test("the mock demos one pending suggestion per metric signal, rendered from params", async () => {
  const submitted = [];
  const m = new MockInitiatives(async (text) => { submitted.push(text); return "req-1"; }, spec);
  const list = await m.handle("GET", ["initiatives"], new URLSearchParams("status=pending"));
  assert.deepEqual(list.map((i) => i.signal).sort(), [...METRIC_SIGNALS].sort());
  for (const it of list) assert.ok(signalText(it.signal, it.params), `${it.signal} has params`);
  const acc = await m.handle("POST", ["initiatives", list[0].id, "accept"], new URLSearchParams());
  assert.equal(acc.status, "accepted");
  assert.equal(acc.request_id, "req-1");
  assert.deepEqual(submitted, [list[0].prompt]);
  await assert.rejects(m.handle("POST", ["initiatives", list[0].id, "accept"], new URLSearchParams()));
  const left = await m.handle("GET", ["initiatives"], new URLSearchParams("status=pending"));
  assert.equal(left.length, 3);
});

test("the mock settings validate thresholds like the backend and merge signals", async () => {
  const m = new MockInitiatives(async () => "r", spec);
  const s = await m.handle("GET", ["settings", "initiative"], new URLSearchParams());
  assert.equal(s.signals.metric_change.enabled, true);
  const out = await m.handle("PUT", ["settings", "initiative"], new URLSearchParams(), { ...s, signals: { quality_drop: { enabled: false } } });
  assert.equal(out.signals.quality_drop.enabled, false);
  assert.equal(out.signals.quality_drop.drop_points, 10);
  assert.equal(out.signals.spend_pace.enabled, true);
  await assert.rejects(m.handle("PUT", ["settings", "initiative"], new URLSearchParams(), { ...s, signals: { spend_pace: { pace_pct: 10 } } }));
});
