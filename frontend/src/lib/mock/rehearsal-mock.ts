/**
 * Mock of the rehearsal (B9, "Ensayo general") for NEXT_PUBLIC_MOCK=true. Same REST shapes and events as
 * backend/internal/api/rehearsal.go: per-agent switches, rehearsal requests (POST /requests {rehearsal:true}), the
 * "would have done" log and the human verdicts. The engine asks `rehearsing()` before an action that would need an
 * approval (the $50,000 proposal send): in a rehearsal it is recorded here and never "sent".
 */
type Emit = (type: string, payload: unknown, agentId?: string) => void;

interface MockAction {
  id: string; request_id: string; task_id: string; agent_id: string; tool: string; action: string; source: string; scope: string;
  would_decision: string; deny_reason?: string; risk: string; reversibility: string; account: string; recipients: string[];
  amount: number | null; flags: string[]; preview: Record<string, string>; args_hash: string;
  verdict: string; verdict_by?: string; verdict_at?: string | null; created_at: string;
}

let n = 0;
const id = (p: string) => `${p}-${Date.now().toString(36)}${(n++).toString(36)}`;

/** "$50,000" in a description -> 50000 (the mock's tool requests carry the amount in text). */
export function amountIn(text: string): number | null {
  const m = /\$\s?([\d][\d,]*(?:\.\d+)?)/.exec(text || "");
  return m ? Number(m[1].replace(/,/g, "")) : null;
}

export class MockRehearsal {
  private agents = new Map<string, { enabled: boolean; set_by: string; updated_at: string }>();
  private requests = new Set<string>();
  private actions: MockAction[] = [];
  private emit: Emit;
  constructor(emit: Emit) { this.emit = emit; } // no parameter property: node's type stripping runs this file in tests

  reset() { this.requests.clear(); this.actions = []; } // switches are configuration: they survive a demo reset

  markRequest(requestId: string) { this.requests.add(requestId); }
  rehearsing(agentId: string, requestId: string): "request" | "agent" | "" {
    if (this.requests.has(requestId)) return "request";
    return this.agents.get(agentId)?.enabled ? "agent" : "";
  }

  record(scope: string, requestId: string, taskId: string, agentId: string, a: { action: string; title: string; details: string; risk: string }) {
    const act: MockAction = {
      id: id("rh"), request_id: requestId, task_id: taskId, agent_id: agentId, tool: "email", action: a.action, source: "simulated", scope,
      would_decision: "needs_approval", risk: a.risk, reversibility: "none", account: "", recipients: [], amount: amountIn(a.details),
      flags: ["always_approval"], preview: { subject: a.title, details: a.details.slice(0, 160) }, args_hash: "",
      verdict: "", created_at: new Date().toISOString(),
    };
    this.actions.unshift(act);
    this.emit("rehearsal.recorded", { id: act.id, request_id: requestId, task_id: taskId, agent_id: agentId, tool: act.tool, action: act.action, would_decision: act.would_decision, scope }, agentId);
    return act;
  }

  matches(method: string, seg: string[]): boolean {
    const [r0, r1, r2, r3] = seg;
    return (r0 === "rehearsal") || (method === "PUT" && r0 === "agents" && !!r1 && r2 === "rehearsal") || (method === "GET" && r0 === "requests" && !!r1 && r2 === "rehearsal" && !r3);
  }

  handle(method: string, seg: string[], q: URLSearchParams, body: any): unknown {
    const [r0, r1, r2, r3] = seg;
    if (r0 === "agents") {
      if (typeof body?.enabled !== "boolean") throw new Error("enabled is required");
      const v = { enabled: body.enabled, set_by: "user", updated_at: new Date().toISOString() };
      this.agents.set(r1, v);
      this.emit("rehearsal.agent_changed", { agent_id: r1, enabled: v.enabled, by: "user" }, r1);
      return { agent_id: r1, ...v };
    }
    if (r0 === "requests") {
      const list = this.actions.filter((a) => a.request_id === r1);
      return { request_id: r1, rehearsal: this.requests.has(r1), actions: list, summary: this.count(list) };
    }
    if (r1 === "agents") {
      const ids = new Set([...this.agents.keys(), ...this.actions.map((a) => a.agent_id)]);
      return {
        agents: [...ids].sort().map((agent) => {
          const c = this.count(this.actions.filter((a) => a.agent_id === agent));
          const sw = this.agents.get(agent);
          return { agent_id: agent, enabled: !!sw?.enabled, set_by: sw?.set_by, updated_at: sw?.updated_at ?? null, actions: c.total, would_approve: c.would_approve, would_reject: c.would_reject, undecided: c.undecided };
        }),
      };
    }
    if (r1 === "actions" && r2 && r3 === "verdict" && method === "POST") {
      const a = this.actions.find((x) => x.id === r2);
      if (!a) throw new Error("not found");
      const v = String(body?.verdict ?? "");
      if (!["", "would_approve", "would_reject"].includes(v)) throw new Error("invalid verdict");
      a.verdict = v; a.verdict_by = v ? "user" : ""; a.verdict_at = v ? new Date().toISOString() : null;
      this.emit("rehearsal.verdict", { id: a.id, request_id: a.request_id, agent_id: a.agent_id, verdict: v }, a.agent_id);
      return { ...a };
    }
    if (r1 === "actions") {
      const req = q.get("request_id"), agent = q.get("agent_id"), limit = Number(q.get("limit") || 500);
      return { items: this.actions.filter((a) => (!req || a.request_id === req) && (!agent || a.agent_id === agent)).slice(0, limit) };
    }
    throw new Error("not found");
  }

  private count(list: MockAction[]) {
    return {
      total: list.length,
      would_approve: list.filter((a) => a.verdict === "would_approve").length,
      would_reject: list.filter((a) => a.verdict === "would_reject").length,
      undecided: list.filter((a) => !a.verdict).length,
    };
  }
}
