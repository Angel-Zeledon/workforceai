import test from "node:test";
import assert from "node:assert/strict";
import { ADD_CLIPS, SIT_S, STAND_S, addWeights, gestureClip, nextBase, outfitFor, startBase, walkTimeScale } from "./studioLogic.ts";

const run = (state, want, secs, step = 0.1) => {
  let s = state, hold = false;
  for (let t = 0; t < secs - 1e-9; t += step) ({ state: s, hold } = nextBase(s, want, step));
  return { s, hold };
};

test("a person who stands and then has to sit plays SitDown and holds the seated pose", () => {
  let s = startBase(false);
  s = nextBase(s, { walking: true, seated: false }, 0.1).state;
  assert.equal(s.base, "Walk");
  s = nextBase(s, { walking: false, seated: true }, 0.1).state;
  assert.equal(s.base, "SitDown");
  assert.equal(run(s, { walking: false, seated: true }, SIT_S + 0.2).s.base, "Seated");
});

test("getting up plays StandUp, freezes the simulation for it, then walks", () => {
  const first = nextBase(startBase(true), { walking: true, seated: false }, 0.05);
  assert.equal(first.state.base, "StandUp");
  assert.equal(first.hold, true);
  const mid = run(first.state, { walking: true, seated: false }, STAND_S - 0.3);
  assert.equal(mid.hold, true);
  const end = run(first.state, { walking: true, seated: false }, STAND_S + 0.2);
  assert.equal(end.s.base, "Walk");
  assert.equal(end.hold, false);
});

test("leaving the seat before sitting down ends in StandUp, and a seated person never walks without standing", () => {
  let s = { base: "SitDown", t: 0.3 };
  s = nextBase(s, { walking: true, seated: false }, 0.05).state;
  assert.equal(s.base, "StandUp");
  for (let i = 0; i < 40; i++) { s = nextBase(s, { walking: true, seated: false }, 0.05).state; assert.notEqual(s.base, "Seated"); }
});

test("typing only while seated at the desk and working; talking never while walking; nothing while sitting down", () => {
  const base = { state: "working", talking: false, gesture: null, env: 0, base: "Seated" };
  assert.equal(addWeights({ ...base, mode: "seat:working" }).type, 1);
  assert.equal(addWeights({ ...base, mode: "seat:meeting" }).type, 0);
  assert.equal(addWeights({ ...base, mode: "seat:thinking", state: "thinking" }).type, 0);
  assert.equal(addWeights({ ...base, mode: "walk", talking: true }).talk, 0);
  assert.equal(addWeights({ ...base, mode: "stand:talking", talking: true }).talk, 1);
  const sitting = addWeights({ ...base, mode: "seat:working", base: "SitDown", talking: true });
  assert.ok(ADD_CLIPS.every((a) => sitting[a] === 0));
});

test("gestures map to a clip with the weight of their envelope", () => {
  assert.equal(gestureClip("headShake"), "shake");
  assert.equal(gestureClip("scratch"), null);
  const w = addWeights({ mode: "stand:idle", state: "idle", talking: false, gesture: "nod", env: 0.6, base: "Idle" });
  assert.equal(w.nod, 0.6);
});

test("outfits are stable per agent, sober and use the right person", () => {
  const a = outfitFor("agent-7", false), b = outfitFor("agent-7", false);
  assert.deepEqual(a, b);
  assert.equal(a.model, "suit");
  assert.notEqual(outfitFor("x", true).model, "suit");
  assert.ok(a.height >= 0.95 && a.height <= 1.05);
  assert.match(a.shirt, /^#[0-9a-f]{6}$/i);
});

test("walkTimeScale: unchanged up to the agents' pace, proportional above it (faster walkers do not skate), capped", () => {
  assert.equal(walkTimeScale(0), 0.45);
  assert.ok(Math.abs(walkTimeScale(1) - 1.2) < 1e-9);
  assert.ok(Math.abs(walkTimeScale(0.5) - 0.825) < 1e-9);
  assert.ok(walkTimeScale(1.52) > walkTimeScale(1));
  assert.ok(Math.abs(walkTimeScale(1.5) / walkTimeScale(1) - 1.5) < 1e-9);
  assert.equal(walkTimeScale(9), 2.4);
});
