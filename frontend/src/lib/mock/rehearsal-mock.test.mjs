// Unit tests of the rehearsal mock (run: npm run test:unit): same shapes as backend/internal/api/rehearsal.go.
import assert from "node:assert/strict";
import { test } from "node:test";
import { MockRehearsal, amountIn } from "./rehearsal-mock.ts";

const send = { action: "send_proposal", title: "Enviar propuesta", details: "Propuesta final: ticket $50,000, margen 24%", risk: "high" };

test("a rehearsal request or an agent switch rehearses; the action is recorded with its amount", () => {
  const events = [];
  const m = new MockRehearsal((type, payload) => events.push({ type, payload }));
  assert.equal(m.rehearsing("sales", "r1"), "");
  m.markRequest("r1");
  assert.equal(m.rehearsing("sales", "r1"), "request");
  m.handle("PUT", ["agents", "legal", "rehearsal"], new URLSearchParams(), { enabled: true });
  assert.equal(m.rehearsing("legal", "r2"), "agent");
  const a = m.record("request", "r1", "t1", "sales", send);
  assert.equal(a.amount, 50000);
  assert.equal(a.would_decision, "needs_approval");
  assert.ok(events.some((e) => e.type === "rehearsal.recorded" && e.payload.id === a.id));
  const rep = m.handle("GET", ["requests", "r1", "rehearsal"], new URLSearchParams(), undefined);
  assert.equal(rep.rehearsal, true);
  assert.equal(rep.summary.undecided, 1);
});

test("verdicts are validated and summarized per agent", () => {
  const m = new MockRehearsal(() => {});
  const a = m.record("agent", "r1", "t1", "sales", send);
  assert.throws(() => m.handle("POST", ["rehearsal", "actions", a.id, "verdict"], new URLSearchParams(), { verdict: "maybe" }));
  m.handle("POST", ["rehearsal", "actions", a.id, "verdict"], new URLSearchParams(), { verdict: "would_reject" });
  const { agents } = m.handle("GET", ["rehearsal", "agents"], new URLSearchParams(), undefined);
  assert.deepEqual(agents.map((x) => [x.agent_id, x.would_reject, x.actions]), [["sales", 1, 1]]);
  m.reset();
  assert.equal(m.handle("GET", ["rehearsal", "actions"], new URLSearchParams(), undefined).items.length, 0);
});

test("amountIn reads dollar amounts in text", () => {
  assert.equal(amountIn("ticket $50,000"), 50000);
  assert.equal(amountIn("sin importe"), null);
});
