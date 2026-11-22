import type { CascadeTask, StallDetail, StalledTask } from "../watchdogApi";

/**
 * Stalled-work watchdog in mock mode (NEXT_PUBLIC_MOCK=true): one task of Contabilidad that stopped 40 minutes ago,
 * with one dependent, the same one the stalled-task suggestion of initiative-mock.ts names. Retry / reassign / cancel
 * resolve it once (a second try conflicts, like the backend); cancel needs the exact cascade.
 */

export const MOCK_STALL_TASK = "t-stall-close";

const cascade = (): CascadeTask[] => [{ id: "t-stall-report", title: "Informe de cierre de agosto", agent_id: "assistant", status: "pending" }];

function stalled(): StalledTask {
  return {
    task_id: MOCK_STALL_TASK, request_id: "req-stall", agent_id: "accounting", title: "Conciliar extractos de agosto", status: "running",
    kind: "no_progress", project: false, last_progress: new Date(Date.now() - 40 * 60_000).toISOString(), minutes: 40, episode: `${MOCK_STALL_TASK}:1`,
  };
}

export class MockStalls {
  private open = true;

  reset() { this.open = true; }

  matches(seg: string[]) { return seg[0] === "stalled-work" || (seg[0] === "tasks" && seg[2] === "stall"); }

  async handle(method: string, seg: string[], body?: any): Promise<unknown> {
    if (seg[0] === "stalled-work") {
      if (method === "GET") return this.open ? [stalled()] : [];
      if (method === "POST" && seg[1] === "scan") return { results: [], raised: 0 };
    }
    const id = seg[1];
    if (id !== MOCK_STALL_TASK) throw new Error("not found");
    if (method === "GET" && seg.length === 3) return this.detail();
    if (method === "POST" && seg.length === 4) {
      if (!this.open) throw new Error("conflict: not stalled");
      const action = seg[3];
      if (action === "cancel") {
        const want = cascade().map((c) => c.id);
        const got: string[] = Array.isArray(body?.confirm_task_ids) ? body.confirm_task_ids : [];
        if (got.length !== want.length || want.some((x) => !got.includes(x))) throw new Error("conflict: cascade_changed");
        this.open = false;
        return { action, status: "applied", cancelled: want };
      }
      if (action === "retry" || action === "reassign") {
        if (action === "reassign" && !body?.agent_id) throw new Error("invalid agent");
        this.open = false;
        return { action, status: "applied" };
      }
    }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }

  private detail(): StallDetail {
    const s = stalled();
    return {
      task: { id: s.task_id, title: s.title, agent_id: s.agent_id, status: this.open ? "running" : "pending", request_id: s.request_id },
      stalled: this.open, stall: this.open ? s : undefined, reassign_candidates: [],
      cancel_cascade: this.open ? cascade() : [], hidden_cascade: 0,
    };
  }
}
