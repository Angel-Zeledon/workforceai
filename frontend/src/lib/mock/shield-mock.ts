/**
 * Mock of the visible shield (B10) for NEXT_PUBLIC_MOCK=true: POST /demo/security-alert plays an email read whose
 * content tries to give an agent orders, exactly as the backend reports it (security.alert with ids only + the
 * security.injection_suspected audit entry), GET /audit?type=... serves the security log and GET /agents/{id}/traces
 * gives the thought audit something to point at. The injected text itself is never part of any of it.
 */
import type { Agent } from "../types";

type Emit = (type: string, payload: unknown, agentId?: string) => void;

interface AuditRow { seq: number; id: string; ts: string; org_id: string; actor: string; action: string; entity: string; entity_id: string; request_id: string; details: Record<string, unknown>; prev_hash: string; hash: string }
interface MockTrace { id: string; agent_id: string; task_id: string; request_id: string; created_at: string }

let n = 0;
const id = (p: string) => `${p}-${Date.now().toString(36)}${(n++).toString(36)}`;

export class MockShield {
  private audit: AuditRow[] = [];
  private traces: MockTrace[] = [];
  constructor(private emit: Emit, private agents: () => Agent[], private org: string) {}

  reset() { this.audit = []; this.traces = []; }

  matches(method: string, seg: string[]): boolean {
    const [r0, r1, r2] = seg;
    return (method === "GET" && r0 === "audit" && !r1)
      || (method === "POST" && r0 === "demo" && r1 === "security-alert")
      || (method === "GET" && r0 === "agents" && !!r1 && r2 === "traces");
  }

  handle(method: string, seg: string[], q: URLSearchParams): unknown {
    const [r0, r1, , r3] = seg;
    if (r0 === "audit") {
      const type = q.get("type") || q.get("action");
      const limit = Number(q.get("limit") || 50);
      const items = this.audit.filter((a) => !type || a.action === type).slice().reverse().slice(0, limit);
      return { items };
    }
    if (r0 === "demo") return this.trigger();
    // thought audit of the agent (only the traces of the demo alerts exist in the mock)
    const mine = this.traces.filter((t) => t.agent_id === r1).slice().reverse();
    if (r3) {
      const t = mine.find((x) => x.id === r3);
      if (!t) throw new Error("not found");
      return this.trace(t);
    }
    return { traces: mine.map((t) => this.trace(t)), store_prompts: false, retention_days: 30 };
  }

  /** An agent (the busy one, or Valeria from sales) reads its inbox; one email tries to give it orders. */
  private trigger() {
    const agents = this.agents();
    const a = agents.find((x) => x.state === "working" && x.current_task_id) ?? agents.find((x) => x.id === "sales") ?? agents[0];
    if (!a) return { ok: false };
    const taskId = a.current_task_id || id("task");
    const requestId = id("req");
    const ts = new Date().toISOString();
    this.traces.push({ id: id("trace"), agent_id: a.id, task_id: taskId, request_id: requestId, created_at: ts });
    const details = { tool: "email", action: "search", task_id: taskId, request_id: requestId, source: "connection", treated_as_data: true };
    this.audit.push({ seq: this.audit.length + 1, id: id("audit"), ts, org_id: this.org, actor: a.id, action: "security.injection_suspected",
      entity: "connection", entity_id: "conn-gmail-demo", request_id: requestId, details, prev_hash: "", hash: "" });
    this.emit("security.alert", { kind: "prompt_injection_suspected", agent_id: a.id, connection_id: "conn-gmail-demo", ...details }, a.id);
    return { ok: true, agent_id: a.id, task_id: taskId };
  }

  private trace(t: MockTrace) {
    return {
      id: t.id, task_id: t.task_id, request_id: t.request_id, kind: "run_task", attempt: 1, created_at: t.created_at,
      provider: "simulation", model: "mock", input_tokens: 820, output_tokens: 140, cost_usd: 0, latency_ms: 900, status: "ok",
      rationale: "Leí la bandeja de entrada para preparar la respuesta. Uno de los correos contenía instrucciones dirigidas a mí; lo traté como datos del correo y no hice lo que pedía.",
      output_summary: { summary: "Respuesta preparada; un correo sospechoso quedó marcado.", confidence: 0.8, findings: 1, recommendations: 1 },
      tool_requests: [{ tool: "email", action: "search", risk: "low", decision: "allow", rule_id: "", approval_status: "", reason: "" }],
      events: [], steer_notes_applied: 0, prompt_stored: false, truncated: false,
    };
  }
}
