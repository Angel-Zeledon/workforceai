import { test, expect } from "@playwright/test";
import WebSocket from "ws";
import { SCENARIO_TEXT, WS_URL } from "../support/env";
import { api, approveUntilDone, resetDemo } from "../support/api";

/** Tipos definidos en docs/SPEC.md. */
const KNOWN_TYPES = new Set([
  "hello",
  "request.received",
  "request.completed",
  "plan.created",
  "agent.state_changed",
  "task.created",
  "task.started",
  "task.completed",
  "task.failed",
  "task.blocked",
  "message.sent",
  "approval.requested",
  "approval.resolved",
  "report.created",
  "activity.logged",
  "error",
  "metrics.updated",
  // Cost control (additive, docs/architecture/08-api.md section 12)
  "cost.estimated",
  "request.status_changed",
  "budget.warning",
  "budget.exceeded",
  "budget.resumed",
]);

const AGENT_STATES = [
  "idle", "thinking", "working", "waiting", "talking",
  "reviewing", "blocked", "awaiting_approval", "completed", "error",
];

interface Frame {
  id: string;
  type: string;
  ts: string;
  org_id: string;
  agent_id?: string;
  payload: Record<string, any>;
}

const isObj = (v: unknown): v is Record<string, any> => typeof v === "object" && v !== null;

function validatePayload(f: Frame): void {
  const p = f.payload;
  expect(isObj(p), `payload de ${f.type}`).toBe(true);
  switch (f.type) {
    case "hello":
      expect(Array.isArray(p.agents)).toBe(true);
      expect(p.agents).toHaveLength(7);
      for (const a of p.agents) {
        expect(typeof a.id).toBe("string");
        expect(typeof a.name).toBe("string");
        expect(AGENT_STATES).toContain(a.state);
      }
      break;
    case "cost.estimated": {
      const e = p.estimate;
      expect(typeof p.request_id).toBe("string");
      expect(isObj(e)).toBe(true);
      // A range, never a single number
      expect(e.total.min_usd).toBeGreaterThan(0);
      expect(e.total.max_usd).toBeGreaterThan(e.total.min_usd);
      expect(Array.isArray(e.tasks)).toBe(true);
      for (const t of e.tasks) expect(t.max_usd).toBeGreaterThanOrEqual(t.min_usd);
      break;
    }
    case "request.status_changed":
      expect(typeof p.request_id).toBe("string");
      expect(["awaiting_confirmation", "paused", "running"]).toContain(p.status);
      break;
    case "budget.exceeded":
      expect(["org", "request", "agent"]).toContain(p.scope);
      expect(typeof p.message).toBe("string");
      expect(typeof p.resumable).toBe("boolean");
      break;
    case "request.received":
      expect(typeof p.request_id).toBe("string");
      expect(typeof p.text).toBe("string");
      break;
    case "request.completed":
      expect(typeof p.request_id).toBe("string");
      expect(typeof p.report_id).toBe("string");
      break;
    case "plan.created":
      expect(typeof p.request_id).toBe("string");
      expect(Array.isArray(p.tasks)).toBe(true);
      for (const t of p.tasks) {
        expect(typeof t.id).toBe("string");
        expect(typeof t.title).toBe("string");
        expect(typeof t.agent_id).toBe("string");
        expect(Array.isArray(t.depends_on)).toBe(true);
      }
      break;
    case "agent.state_changed":
      expect(typeof p.agent_id).toBe("string");
      expect(AGENT_STATES).toContain(p.state);
      expect(typeof p.activity).toBe("string");
      expect(p.task_id === null || typeof p.task_id === "string").toBe(true);
      expect(typeof p.progress).toBe("number");
      break;
    case "task.created":
    case "task.started":
    case "task.completed":
    case "task.failed":
    case "task.blocked":
      expect(isObj(p.task)).toBe(true);
      expect(typeof p.task.id).toBe("string");
      expect(typeof p.task.agent_id).toBe("string");
      expect(["pending", "running", "blocked", "awaiting_approval", "done", "failed"]).toContain(p.task.status);
      break;
    case "message.sent":
      expect(isObj(p.message)).toBe(true);
      expect(typeof p.message.from).toBe("string");
      expect(typeof p.message.to).toBe("string");
      expect(["chat", "delegation", "consult", "answer"]).toContain(p.message.kind);
      expect(typeof p.message.text).toBe("string");
      break;
    case "approval.requested":
    case "approval.resolved":
      expect(isObj(p.approval)).toBe(true);
      expect(typeof p.approval.id).toBe("string");
      expect(typeof p.approval.action).toBe("string");
      expect(["low", "medium", "high"]).toContain(p.approval.risk);
      expect(["pending", "approved", "rejected"]).toContain(p.approval.status);
      break;
    case "report.created":
      expect(isObj(p.report)).toBe(true);
      expect(typeof p.report.id).toBe("string");
      expect(Array.isArray(p.report.sections)).toBe(true);
      expect(Array.isArray(p.report.contributors)).toBe(true);
      break;
    case "activity.logged":
      expect(isObj(p.item)).toBe(true);
      expect(typeof p.item.id).toBe("string");
      expect(typeof p.item.kind).toBe("string");
      expect(typeof p.item.text).toBe("string");
      break;
    case "error":
      expect(typeof p.message).toBe("string");
      break;
    case "metrics.updated":
      expect(isObj(p.metrics)).toBe(true);
      expect(typeof p.metrics.cost_usd).toBe("number");
      break;
  }
}

test("contrato WS: forma de los eventos del SPEC", async () => {
  await resetDemo();
  const frames: Frame[] = [];
  const ws = new WebSocket(WS_URL);
  ws.on("message", (raw) => frames.push(JSON.parse(raw.toString())));
  await new Promise<void>((resolve, reject) => {
    ws.once("open", () => resolve());
    ws.once("error", reject);
  });

  // 1) hello inmediato
  await expect.poll(() => frames.length, { timeout: 10_000 }).toBeGreaterThan(0);
  expect(frames[0].type).toBe("hello");

  // 2) disparar el escenario y aprobar hasta terminar
  const { request_id } = await api.post<{ request_id: string }>("/requests", { text: SCENARIO_TEXT });
  const final = await approveUntilDone(request_id);
  expect(final.status).toBe("done");
  await expect.poll(() => frames.some((f) => f.type === "request.completed"), { timeout: 10_000 }).toBe(true);
  ws.close();

  // 3) validar envelope y payload de cada frame
  for (const f of frames) {
    expect(typeof f.id, "id").toBe("string");
    expect(KNOWN_TYPES.has(f.type), `tipo desconocido: ${f.type}`).toBe(true);
    expect(Number.isNaN(Date.parse(f.ts)), `ts RFC3339 en ${f.type}`).toBe(false);
    expect(typeof f.org_id).toBe("string");
    validatePayload(f);
  }

  // 4) tipos mínimos esperados del flujo completo
  const seen = new Set(frames.map((f) => f.type));
  for (const t of [
    "hello", "request.received", "plan.created", "task.created", "task.started",
    "task.completed", "agent.state_changed", "approval.requested", "approval.resolved",
    "report.created", "request.completed", "activity.logged", "cost.estimated",
  ]) {
    expect(seen.has(t), `falta evento ${t}`).toBe(true);
  }
});
