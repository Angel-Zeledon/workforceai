// Unit tests of the stalled-work watchdog helpers and its mock (run: npm run test:unit).
import assert from "node:assert/strict";
import fs from "node:fs";
import { test } from "node:test";
import { STALL_SIGNAL, canCancel, confirmIds, mayBeStalled, stallAge, stallDoneKey, stallErrorKey, stallTaskOf } from "./watchdogLogic.ts";
import { MockStalls } from "./mock/watchdog-mock.ts";

const catalog = (l) => JSON.parse(fs.readFileSync(new URL(`./i18n/${l}.json`, import.meta.url), "utf8"));

test("stallTaskOf only reads stalled-task suggestions", () => {
  assert.equal(stallTaskOf(STALL_SIGNAL, { task_id: "t1" }), "t1");
  assert.equal(stallTaskOf("task_failed", { task_id: "t1" }), "");
  assert.equal(stallTaskOf(STALL_SIGNAL, {}), "");
  assert.equal(stallTaskOf(STALL_SIGNAL, null), "");
});

test("mayBeStalled covers running and ready tasks only", () => {
  for (const s of ["running", "pending", "ready"]) assert.ok(mayBeStalled(s), s);
  for (const s of ["done", "failed", "blocked", "awaiting_approval", "needs_input"]) assert.ok(!mayBeStalled(s), s);
});

test("stallAge picks minutes, hours or days", () => {
  assert.deepEqual(stallAge(40), { key: "stall.age.min", n: 40 });
  assert.deepEqual(stallAge(130), { key: "stall.age.hours", n: 2 });
  assert.deepEqual(stallAge(3 * 24 * 60), { key: "stall.age.days", n: 3 });
  assert.deepEqual(stallAge(-5), { key: "stall.age.min", n: 0 });
});

test("cancel confirms exactly the cascade shown and is refused with hidden dependents", () => {
  assert.deepEqual(confirmIds([{ id: "b" }, { id: "c" }]), ["b", "c"]);
  assert.ok(canCancel({ stalled: true, hidden_cascade: 0 }));
  assert.ok(!canCancel({ stalled: true, hidden_cascade: 1 }));
  assert.ok(!canCancel({ stalled: false, hidden_cascade: 0 }));
  assert.ok(!canCancel(null));
});

test("error and done keys exist in es, en and pt", () => {
  const keys = new Set([
    stallErrorKey(409, "cascade_changed"), stallErrorKey(409, "cascade_hidden"), stallErrorKey(403), stallErrorKey(409), stallErrorKey(400),
    stallErrorKey(404), stallErrorKey(500), stallDoneKey("retry", "applied"), stallDoneKey("reassign", "resumed"), stallDoneKey("cancel", "applied"),
    stallDoneKey("retry", "finished"), stallDoneKey("retry", "requested"), "stall.age.min", "stall.age.hours", "stall.age.days",
    "stall.title", "stall.retry", "stall.reassign", "stall.cancel", "stall.pick", "stall.noCandidates", "stall.cancelTitle", "stall.cancelOnly",
    "stall.cancelWith", "stall.cancelConfirm", "stall.keep", "stall.kind.no_progress", "stall.kind.no_worker", "stall.noLonger",
    "initiative.signal.stalled_task.title", "initiative.signal.stalled_task.body", "initiative.signals.stalled_work",
    "initiative.signals.stalled_work.task_minutes", "initiative.signals.stalled_work.project_minutes",
  ]);
  for (const l of ["es", "en", "pt"]) {
    const cat = catalog(l);
    for (const k of keys) assert.ok(cat[k], `${l}: missing ${k}`);
  }
});

test("the mock retries once, conflicts the second time and cancels with the confirmed cascade", async () => {
  const m = new MockStalls();
  const list = await m.handle("GET", ["stalled-work"]);
  assert.equal(list.length, 1);
  const id = list[0].task_id;
  const d = await m.handle("GET", ["tasks", id, "stall"]);
  assert.ok(d.stalled);
  assert.equal(d.cancel_cascade.length, 1);
  await assert.rejects(m.handle("POST", ["tasks", id, "stall", "cancel"], { confirm_task_ids: [] }));
  const r = await m.handle("POST", ["tasks", id, "stall", "retry"], {});
  assert.equal(r.status, "applied");
  await assert.rejects(m.handle("POST", ["tasks", id, "stall", "retry"], {}));
  assert.equal((await m.handle("GET", ["stalled-work"])).length, 0);
  m.reset();
  const c = await m.handle("POST", ["tasks", id, "stall", "cancel"], { confirm_task_ids: d.cancel_cascade.map((x) => x.id) });
  assert.deepEqual(c.cancelled, d.cancel_cascade.map((x) => x.id));
});
