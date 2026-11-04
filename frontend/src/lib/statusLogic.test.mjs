// Unit tests of the single status table (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import fs from "node:fs";
import { KIND_COLOR, KIND_TONE, LABEL_PREFIX, colorTable, kindOf, statusColor } from "./statusLogic.ts";

const cat = (l) => JSON.parse(fs.readFileSync(new URL(`./i18n/${l}.json`, import.meta.url), "utf8"));

test("needs-you is amber in every domain (projects, batch rows, pedidos, agents, tasks)", () => {
  for (const [d, s] of [["node", "needs_input"], ["node", "awaiting_approval"], ["project", "waiting_human"], ["batchRow", "needs_help"], ["pedido", "needs_help"], ["pedido", "needs_approval"], ["agent", "awaiting_approval"], ["task", "awaiting_approval"], ["customer", "waiting_approval"]]) {
    assert.equal(kindOf(d, s), "needs", `${d}.${s}`);
    assert.equal(statusColor(d, s), KIND_COLOR.needs);
  }
});

test("one green for finished: done and completed share the color", () => {
  assert.equal(statusColor("node", "done"), statusColor("pedido", "done"));
  assert.equal(statusColor("agent", "completed"), statusColor("project", "done"));
  assert.equal(statusColor("batchRow", "partial"), KIND_COLOR.done);
});

test("ready is 'to start', never confused with done", () => {
  assert.equal(kindOf("project", "ready"), "todo");
  assert.equal(kindOf("node", "ready"), "todo");
  assert.notEqual(statusColor("project", "ready"), statusColor("pedido", "done"));
});

test("agents: idle is free, thinking/working are running, error/blocked a problem", () => {
  assert.equal(kindOf("agent", "idle"), "free");
  for (const s of ["thinking", "working", "reviewing", "talking"]) assert.equal(kindOf("agent", s), "running");
  for (const s of ["error", "blocked"]) assert.equal(kindOf("agent", s), "problem");
});

test("unknown states from a newer server fall back to gray", () => {
  assert.equal(kindOf("node", "brand_new_state"), "todo");
  assert.equal(kindOf("pedido", undefined), "todo");
});

test("colorTable derives a per-screen table from the single palette", () => {
  assert.deepEqual(colorTable("task", ["done", "failed"]), { done: KIND_COLOR.done, failed: KIND_COLOR.problem });
  assert.equal(KIND_TONE.needs, "warn");
});

// The words: the same kind reads the same in every screen and language.
const WORDS = {
  es: { todo: "Por empezar", running: "En curso", needs: "Te necesita", paused: "En pausa", done: "Listo", partial: "Listo en parte", problem: "Con problema", cancelled: "Cancelado", draft: "Borrador", free: "Libre" },
  en: { todo: "To start", running: "In progress", needs: "Needs you", paused: "Paused", done: "Done", partial: "Partly done", problem: "Has a problem", cancelled: "Cancelled", draft: "Draft", free: "Free" },
  pt: { todo: "A começar", running: "Em andamento", needs: "Precisa de você", paused: "Em pausa", done: "Pronto", partial: "Pronto em parte", problem: "Com problema", cancelled: "Cancelado", draft: "Rascunho", free: "Livre" },
};
const CHECKED = {
  node: ["draft", "pending", "ready", "running", "awaiting_approval", "needs_input", "paused", "blocked", "done", "failed", "cancelled"],
  project: ["draft", "planning", "ready", "running", "paused", "waiting_human", "done", "partial", "failed", "cancelled"],
  agent: ["idle", "thinking", "working", "awaiting_approval", "blocked", "completed", "error"],
  task: ["pending", "running", "blocked", "awaiting_approval", "done", "failed"],
  pedido: ["running", "needs_help", "needs_approval", "paused", "done", "failed"],
  batchRow: ["queued", "running", "paused", "needs_help", "done", "partial", "failed", "cancelled"],
  batch: ["running", "done", "cancelled"],
};

for (const l of ["es", "en", "pt"]) {
  test(`${l}: every status label starts with the word of its kind`, () => {
    const c = cat(l);
    const bad = [];
    for (const [d, states] of Object.entries(CHECKED)) {
      for (const s of states) {
        const k = LABEL_PREFIX[d] + s, word = WORDS[l][kindOf(d, s)];
        const v = c[k];
        // "Listo" must not swallow "Listo en parte": compare the whole first part before " · "
        if (typeof v !== "string" || v.split(" · ")[0] !== word) bad.push(`${k}=${v} (want ${word})`);
      }
    }
    assert.deepEqual(bad, []);
  });
}

// The few tables that must stay standalone (node runs their tests without the bundler) agree with the palette.
import { rowTone, ROW_STATUSES } from "./batchLogic.ts";
import { STATUS_COLOR as CUSTOMER_COLOR } from "./customersLogic.ts";
import { statusTone } from "./statusLogic.ts";
test("batch rows and customers use the single palette", () => {
  for (const s of ROW_STATUSES) assert.equal(rowTone(s), statusTone("batchRow", s), s);
  for (const [s, c] of Object.entries(CUSTOMER_COLOR)) assert.equal(c, statusColor("customer", s), s);
});
