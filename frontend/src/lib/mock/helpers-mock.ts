import type { HelperGroup, HelperPart, Request, Task } from "../types";
import { clampCount, demoEstimate, rescale, splitItems } from "../helpersLogic";
import { tr } from "../i18n-core";

/** What the engine gives the helpers demo: events, time, the owner and where the tasks live. */
export interface HelpersHost {
  emit: (type: string, payload: unknown, agentId?: string) => void;
  wait: (ms: number) => Promise<void>;
  uid: (p: string) => string;
  assistantId: () => string;
  addTask: (t: Task) => void;
  emitTask: (type: string, t: Task) => void;
  /** the group is over: the responsible delivers what the parts left (in the conversation, with its file) */
  deliver: (g: HelperGroup) => void;
  nowIso: () => string;
}

const MAX_HELPERS = 8;
const PARALLEL = 4;

interface Ctx { text: string; attach: number; free: boolean }

/**
 * Demo of temporary helpers: a request with a list of 4 or more things (or several attachments) gets a proposal; nothing runs
 * until the person accepts. One part fails the first time so "Reintentar" can be seen. Same events and endpoints as the contract.
 */
export class MockHelpers {
  private groups = new Map<string, HelperGroup>();
  private ctx: Ctx | null = null;
  private resume = new Map<string, () => void>(); // request id -> the normal plan, run when the person declines
  private stopped = new Set<string>(); // "group:part" or "group" (all)
  private failedOnce = new Set<string>();
  private source = new Map<string, { items: string[]; files: boolean }>();

  constructor(private h: HelpersHost) {}

  reset() { this.groups.clear(); this.resume.clear(); this.stopped.clear(); this.failedOnce.clear(); this.source.clear(); this.ctx = null; }

  /** Called by POST /messages before the chat routes the text (the request is created a moment later). */
  note(text: string, attach: number, free: boolean) { this.ctx = { text: text.trim(), attach, free }; }

  matches(seg: string[]) { return seg[0] === "requests" && seg[2] === "helpers"; }
  groupOf(requestId: string): HelperGroup | undefined { return [...this.groups.values()].filter((g) => g.request_id === requestId).pop(); }
  all(): HelperGroup[] { return [...this.groups.values()].map((g) => ({ ...g })); }

  /** True when a proposal was made (the normal plan is held until it is declined). `resume` runs the plan. */
  propose(req: Request, ownerForPosition: string, resume: () => void): boolean {
    const c = this.ctx;
    if (!c || c.text !== req.text.trim()) return false;
    this.ctx = null;
    const items = splitItems(c.text, c.attach);
    if (items.length < 4) return false;
    const count = clampCount(Math.min(items.length, MAX_HELPERS), { max_count: MAX_HELPERS });
    const owner = c.free ? this.h.assistantId() : ownerForPosition;
    const files = c.attach > 0 && items.every((x) => /^\d+$/.test(x));
    const parts = this.partsFor(items, count, files);
    const g: HelperGroup = {
      id: this.h.uid("hg"), request_id: req.id, owner_agent_id: owner, mode: c.free ? "free" : "position", count, max_count: MAX_HELPERS,
      parallel: Math.min(PARALLEL, count), parts, ...demoEstimate(count), spent_usd: 0, status: "proposed",
    };
    this.groups.set(g.id, g);
    this.source.set(g.id, { items, files });
    this.resume.set(req.id, resume);
    this.h.emit("helpers.proposed", { group: { ...g } }, owner);
    return true;
  }

  private partsFor(items: string[], count: number, files: boolean): HelperPart[] {
    const per = Math.ceil(items.length / count);
    return Array.from({ length: count }, (_, i) => {
      const from = i * per + 1, to = Math.min(items.length, (i + 1) * per);
      const label = from > to ? tr("helpers.part.empty", { n: i + 1 })
        : from === to ? (files ? tr("helpers.part.file", { n: from }) : items[from - 1].slice(0, 60))
        : tr(files ? "helpers.part.files" : "helpers.part.items", { from, to });
      return { index: i + 1, label, status: "pending" };
    });
  }

  private publish(g: HelperGroup) { this.h.emit("helpers.updated", { group: { ...g, parts: g.parts.map((p) => ({ ...p })) } }, g.owner_agent_id); }

  async handle(method: string, seg: string[], body: any): Promise<unknown> {
    const g = this.groupOf(seg[1]);
    if (!g || method !== "POST") throw new Error(`${method} /${seg.join("/")} -> 404`);
    const action = seg[3] ?? "";
    if (action === "") { // launch
      if (g.status !== "proposed") return { group: { ...g } }; // idempotent: a double click launches once
      const count = clampCount(Number(body?.count) || g.count, g);
      if (count !== g.count) this.resize(g, count);
      g.cap_usd = Math.min(g.cap_usd || Infinity, Number(body?.cap_usd) || g.cap_usd) || g.cap_usd;
      g.status = "running"; g.accepted_by = "user";
      this.resume.delete(g.request_id);
      this.publish(g);
      void this.run(g).catch(() => { /* cancelled by reset */ });
      return { group: { ...g } };
    }
    if (action === "decline") {
      if (g.status === "proposed") { g.status = "declined"; this.publish(g); const r = this.resume.get(g.request_id); this.resume.delete(g.request_id); r?.(); }
      return { group: { ...g } };
    }
    if (action === "stop") {
      const part = body?.part;
      if (part == null) { this.stopped.add(g.id); for (const p of g.parts) if (p.status === "pending") p.status = "stopped"; if (g.status === "running" || g.status === "joining") g.status = "stopped"; }
      else { this.stopped.add(`${g.id}:${part}`); const p = g.parts.find((x) => x.index === Number(part)); if (p && p.status === "pending") p.status = "stopped"; }
      this.publish(g);
      void this.finish(g).catch(() => { /* cancelled by reset */ }); // nothing left running: the responsible hands over what there is
      return { group: { ...g } };
    }
    if (action === "retry") {
      const p = g.parts.find((x) => x.index === Number(body?.part));
      if (p && (p.status === "failed" || p.status === "stopped")) {
        p.status = "pending";
        this.stopped.delete(`${g.id}:${p.index}`);
        if (g.status === "stopped" || g.status === "failed") g.status = "running";
        this.publish(g);
        void this.runPart(g, p).then(() => this.finish(g)).catch(() => { /* cancelled by reset */ });
      }
      return { group: { ...g } };
    }
    throw new Error(`POST /${seg.join("/")} -> 404`);
  }

  private resize(g: HelperGroup, count: number) {
    const r = rescale(g, count);
    const src = this.source.get(g.id);
    g.parts = src ? this.partsFor(src.items, count, src.files) : g.parts.slice(0, count);
    g.count = count; g.parallel = Math.min(PARALLEL, count); g.estimate = r.estimate; g.cap_usd = r.cap_usd;
  }

  private taskOf(g: HelperGroup, p: HelperPart): Task {
    return {
      id: p.task_id ?? this.h.uid("task"), request_id: g.request_id, workflow_id: null, title: p.label, description: p.label, agent_id: g.owner_agent_id,
      status: "pending", depends_on: [], parent_task_id: null, created_at: this.h.nowIso(), started_at: null, finished_at: null, output: null,
      helper: { group_id: g.id, index: p.index, total: g.count, label: p.label },
    };
  }

  private async run(g: HelperGroup) {
    for (const p of g.parts) { const t = this.taskOf(g, p); p.task_id = t.id; this.h.addTask(t); this.h.emitTask("task.created", t); }
    this.publish(g);
    const queue = g.parts.filter((p) => p.status === "pending");
    const lane = async () => {
      for (;;) {
        if (this.stopped.has(g.id)) return;
        const p = queue.shift();
        if (!p) return;
        if (p.status !== "pending") continue;
        await this.runPart(g, p);
      }
    };
    await Promise.all(Array.from({ length: g.parallel }, lane));
    await this.finish(g);
  }

  private async runPart(g: HelperGroup, p: HelperPart) {
    if (!p.task_id) { const t0 = this.taskOf(g, p); p.task_id = t0.id; this.h.addTask(t0); this.h.emitTask("task.created", t0); }
    const t = { ...this.taskOf(g, p), id: p.task_id! };
    t.status = "running"; t.started_at = this.h.nowIso(); p.status = "running";
    this.h.emitTask("task.started", t); this.publish(g);
    await this.h.wait(1800 + (p.index % 3) * 600);
    const fail = p.index === 3 && g.count >= 3 && !this.failedOnce.has(g.id);
    if (fail) {
      this.failedOnce.add(g.id);
      p.status = "failed"; t.status = "failed"; t.finished_at = this.h.nowIso();
      this.h.emitTask("task.failed", t);
    } else {
      p.status = "done"; t.status = "done"; t.finished_at = this.h.nowIso();
      t.output = { summary: tr("helpers.part.summary", { label: p.label }), findings: [], metrics: {}, hypotheses: [], evidence: [], recommendations: [], confidence: 0.8, suggested_tasks: [] };
      g.spent_usd = Math.round((g.spent_usd + g.estimate.max_usd / g.count * 0.7) * 1000) / 1000;
      this.h.emitTask("task.completed", t);
      this.h.emit("file.activity", { agent_id: g.owner_agent_id, task_id: t.id, action: "write", path: `/Helpers/${g.id}/part-${p.index}.md` }, g.owner_agent_id);
    }
    // the cap is hard: when it is reached the parts that did not start are stopped
    if (g.cap_usd > 0 && g.spent_usd >= g.cap_usd) { this.stopped.add(g.id); for (const q of g.parts) if (q.status === "pending") q.status = "stopped"; }
    this.publish(g);
  }

  private async finish(g: HelperGroup) {
    if (g.parts.some((p) => p.status === "running" || p.status === "pending") && !this.stopped.has(g.id)) return;
    if (g.parts.some((p) => p.status === "running")) return;
    if (g.parts.some((p) => p.status === "failed") && !this.stopped.has(g.id)) return; // waits for Reintentar or Detener
    if (g.status === "joining" || g.status === "done") return;
    g.status = "joining"; this.publish(g);
    const join: Task = {
      id: this.h.uid("task"), request_id: g.request_id, workflow_id: null, title: tr("helpers.join.title"), description: tr("helpers.join.title"), agent_id: g.owner_agent_id,
      status: "running", depends_on: g.parts.map((p) => p.task_id!).filter(Boolean), parent_task_id: null, created_at: this.h.nowIso(), started_at: this.h.nowIso(), finished_at: null, output: null,
    };
    this.h.addTask(join); this.h.emitTask("task.created", join); this.h.emitTask("task.started", join);
    await this.h.wait(1800);
    join.status = "done"; join.finished_at = this.h.nowIso();
    const n = g.parts.filter((p) => p.status === "done").length;
    join.output = { summary: tr("helpers.join.summary", { done: n, total: g.parts.length }), findings: [], metrics: {}, hypotheses: [], evidence: [], recommendations: [], confidence: 0.82, suggested_tasks: [] };
    this.h.emitTask("task.completed", join);
    g.status = this.stopped.has(g.id) && n < g.parts.length ? "stopped" : "done";
    this.publish(g);
    this.h.deliver({ ...g, parts: g.parts.map((p) => ({ ...p })) });
  }
}
