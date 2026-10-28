// Unit tests of the team debate playback in the meeting room (B5; run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  BUBBLE_SHOW_MS, DEBATE_GATHER_MS, DEBATE_LINGER_MS, DEBATE_TURN_MS, DEBATE_WAIT_MAX_MS, debateSeats, debateStage, liveDebate, stepBubbles, zoneFor,
} from "./officeState.ts";

const turns = [
  { agent_id: "sales", kind: "position", text: "A" },
  { agent_id: "legal", kind: "objection", text: "B" },
  { agent_id: "assistant", kind: "synthesis", text: "C" },
];
const clock = (o = {}) => ({ id: "d1", status: "done", startedAt: 0, doneAt: 1000, participants: ["sales", "legal"], turns, ...o });

test("debate: gather, one turn after another, linger, then over", () => {
  assert.equal(debateStage(clock({ status: "running", doneAt: null }), 500).phase, "gathering");
  assert.equal(debateStage(clock(), DEBATE_GATHER_MS - 1).phase, "gathering"); // the team is still walking in
  const t0 = DEBATE_GATHER_MS;
  assert.deepEqual(debateStage(clock(), t0), { active: true, phase: "talking", turn: 0, speaker: "sales", text: "A", kind: "position" });
  assert.equal(debateStage(clock(), t0 + DEBATE_TURN_MS).speaker, "legal");
  assert.equal(debateStage(clock(), t0 + 2 * DEBATE_TURN_MS).kind, "synthesis");
  assert.equal(debateStage(clock(), t0 + 3 * DEBATE_TURN_MS).phase, "closing");
  assert.equal(debateStage(clock(), t0 + 3 * DEBATE_TURN_MS + DEBATE_LINGER_MS).active, false);
  // a late completion starts the playback when it arrives
  assert.equal(debateStage(clock({ doneAt: 20000 }), 20000).turn, 0);
});

test("debate: a failed or never-completed debate ends (everyone walks back)", () => {
  assert.equal(debateStage(clock({ status: "failed" }), 10).active, false);
  assert.equal(debateStage(clock({ status: "running", doneAt: null }), DEBATE_WAIT_MAX_MS + 1).active, false);
});

test("debate: the newest active debate wins and the moderator sits first", () => {
  const a = clock({ id: "a", startedAt: 0 }), b = clock({ id: "b", startedAt: 100, doneAt: null, status: "running" });
  assert.equal(liveDebate([a, b], 200).clock.id, "b");
  assert.equal(liveDebate([a], DEBATE_WAIT_MAX_MS * 2), null);
  assert.deepEqual(debateSeats(clock({ participants: ["sales", "assistant", "legal"] }), "assistant"), ["assistant", "sales", "legal"]);
  assert.deepEqual(debateSeats(clock(), null), ["sales", "legal"]);
});

test("debate seats win over a finished or busy state, not over a block, an approval or a budget pause", () => {
  const base = { budgetPaused: false, planning: true, approvalPending: false, debating: true };
  for (const state of ["completed", "working", "talking", "idle", "error"]) assert.equal(zoneFor({ ...base, state }), "meeting", state);
  assert.equal(zoneFor({ ...base, state: "blocked" }), "desk");
  assert.equal(zoneFor({ ...base, state: "awaiting_approval", approvalPending: true }), "approvals");
  assert.equal(zoneFor({ ...base, state: "working", budgetPaused: true }), "cafeteria");
  assert.equal(zoneFor({ ...base, state: "completed", debating: false }), "desk"); // unchanged without a debate
});

test("debate bubbles follow the debate clock: no quiet gap, no repeat", () => {
  const st = new Map();
  const b = (key) => ({ key, kind: "debate", text: key, live: true });
  assert.deepEqual(stepBubbles(st, [{ id: "sales", bubble: b("d1:0") }], 0), ["sales"]);
  // the same speaker again a moment later with the next turn: shown at once (no gap)
  assert.deepEqual(stepBubbles(st, [{ id: "sales", bubble: b("d1:3") }], 1000), ["sales"]);
  assert.equal(st.get("sales").text, "d1:3");
  // the turn moved to someone else: the previous speaker goes quiet immediately
  assert.deepEqual(stepBubbles(st, [{ id: "sales", bubble: null }, { id: "legal", bubble: b("d1:4") }], 1500), ["legal"]);
  // never repeated once it expired
  assert.deepEqual(stepBubbles(st, [{ id: "legal", bubble: b("d1:4") }], 1500 + BUBBLE_SHOW_MS + 1), []);
});
