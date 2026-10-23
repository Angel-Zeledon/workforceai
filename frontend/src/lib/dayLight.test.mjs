// Unit tests of the "automatic (real time)" office light (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { MOOD_BASE, dayPhase, localHour, timeOfDayMix } from "./officeLayoutModel.ts";

test("dayPhase splits the local day into dawn, day, dusk and night", () => {
  assert.equal(dayPhase(3), "night");
  assert.equal(dayPhase(5), "dawn");
  assert.equal(dayPhase(7.9), "dawn");
  assert.equal(dayPhase(8), "day");
  assert.equal(dayPhase(12), "day");
  assert.equal(dayPhase(17), "dusk");
  assert.equal(dayPhase(20.5), "dusk");
  assert.equal(dayPhase(21), "night");
  assert.equal(dayPhase(23.99), "night");
  assert.equal(dayPhase(-1), "night");
  assert.equal(dayPhase(25), "night"); // 1 am
});

test("timeOfDayMix is the preset at the keyframes and a blend in between", () => {
  assert.deepEqual({ ...timeOfDayMix(12), phase: undefined }, { ...MOOD_BASE.natural, phase: undefined });
  assert.equal(timeOfDayMix(2).bg, MOOD_BASE.night.bg);
  assert.equal(timeOfDayMix(2).sun, MOOD_BASE.night.sun);
  const dusk = timeOfDayMix(18);
  assert.equal(dusk.phase, "dusk");
  assert.ok(dusk.sun > Math.min(MOOD_BASE.natural.sun, MOOD_BASE.sunset.sun) - 1e-9 && dusk.sun < Math.max(MOOD_BASE.natural.sun, MOOD_BASE.sunset.sun) + 1e-9);
  assert.match(dusk.bg, /^#[0-9a-f]{6}$/);
  // night is darker than noon, dawn sits in between
  assert.ok(timeOfDayMix(23).amb < timeOfDayMix(6).amb && timeOfDayMix(6).amb < timeOfDayMix(12).amb);
  // continuous around midnight
  assert.equal(timeOfDayMix(0).bg, timeOfDayMix(23.99).bg);
});

test("localHour reads the viewer's clock", () => {
  const d = new Date(2026, 9, 23, 18, 30);
  assert.equal(localHour(d), 18.5);
});
