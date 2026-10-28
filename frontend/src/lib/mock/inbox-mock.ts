import { tr } from "../i18n-core";
import type { ApprovalInbox, InboxGroup, InboxItem, InboxSuggestion } from "../inboxLogic";
import type { Approval, Task } from "../types";

/** Demo of the fast approvals inbox (B17): pending approvals from three demo projects (one batchable group of
 * identical low-risk items, a medium one, a high-risk one with a cached devil's advocate review) plus one without a
 * project, and a decision history that triggers one "raise the threshold" suggestion. The live approvals of the demo
 * engine are added on top. Same shapes and rules as backend/internal/application/approval_inbox.go. */
interface Demo { id: string; project?: string; action: string; risk: Approval["risk"]; agent: string; key: string; cost: number; counter?: boolean }
const DEMO: Demo[] = [
  { id: "demo-ap-1", project: "a", action: "categorize_expense", risk: "low", agent: "accounting", key: "cat1", cost: 0.004 },
  { id: "demo-ap-2", project: "a", action: "categorize_expense", risk: "low", agent: "accounting", key: "cat2", cost: 0.003 },
  { id: "demo-ap-3", project: "a", action: "categorize_expense", risk: "low", agent: "accounting", key: "cat3", cost: 0.005 },
  { id: "demo-ap-4", project: "b", action: "publish_post", risk: "medium", agent: "sales", key: "post", cost: 0.012 },
  { id: "demo-ap-5", project: "c", action: "send_contract", risk: "high", agent: "legal", key: "contract", cost: 0.021, counter: true },
  { id: "demo-ap-6", action: "pay_invoice", risk: "high", agent: "accounting", key: "invoice", cost: 0.009 },
];
const RANK = { low: 0, medium: 1, high: 2 } as const;

export class MockInbox {
  private decided = new Map<string, "approved" | "rejected">();
  private counters = new Set<string>();
  private created = Date.now();
  constructor(private deps: { approvals: () => Approval[]; tasks: () => Task[] }) {}

  reset() { this.decided.clear(); this.counters.clear(); this.created = Date.now(); }
  private isDemo(id?: string) { return !!id && DEMO.some((d) => d.id === id); }

  matches(method: string, seg: string[]) {
    if (seg[0] !== "approvals") return false;
    if (method === "GET" && seg[1] === "inbox") return true;
    if (method === "POST" && seg[1] === "batch") return true;
    return this.isDemo(seg[1]) && (seg[2] === "decision" || seg[2] === "counterargument");
  }

  private counter(id: string) {
    return { approval_id: id, advisory: true, counterargument: tr("mock.inbox.counter"), risks: [tr("mock.inbox.risk1"), tr("mock.inbox.risk2")],
      checks: [tr("mock.inbox.check1")], cost_usd: 0.004, created_at: new Date().toISOString() };
  }

  private items(): InboxItem[] {
    const out: InboxItem[] = [];
    DEMO.forEach((d, n) => {
      if (this.decided.has(d.id)) return;
      const pid = d.project ? `prj-demo-${d.project}` : undefined;
      out.push({
        id: d.id, task_id: `${d.id}-task`, agent_id: d.agent, action: d.action, title: tr(`mock.inbox.${d.key}.title`), details: tr(`mock.inbox.${d.key}.details`),
        risk: d.risk, status: "pending", created_at: new Date(this.created - (DEMO.length - n) * 60_000).toISOString(), resolved_at: null,
        required_approvals: 1, decisions: [], task_title: tr(`mock.inbox.${d.key}.task`), task_cost_usd: d.cost, high_risk: d.risk === "high",
        ...(pid ? { project_id: pid, project_name: tr(`mock.inbox.project.${d.project}`) } : {}),
        ...(d.counter || this.counters.has(d.id) ? { counterargument: { summary: tr("mock.inbox.counter"), risks: [tr("mock.inbox.risk1"), tr("mock.inbox.risk2")] } } : {}),
      });
    });
    const tasks = new Map(this.deps.tasks().map((t) => [t.id, t]));
    for (const a of this.deps.approvals()) {
      if (a.status !== "pending") continue;
      out.push({ ...a, task_title: tasks.get(a.task_id)?.title, request_id: tasks.get(a.task_id)?.request_id, task_cost_usd: 0, high_risk: a.risk === "high" || (a.required_approvals ?? 1) > 1 });
    }
    return out.sort((x, y) => RANK[y.risk] - RANK[x.risk] || x.created_at.localeCompare(y.created_at));
  }

  private inbox(): ApprovalInbox {
    const items = this.items();
    const groups = new Map<string, InboxGroup>();
    for (const it of items) {
      if (!it.project_id) continue;
      const key = `${it.project_id}|${it.action}`;
      const g = groups.get(key) ?? { key, project_id: it.project_id, project_name: it.project_name, action: it.action, count: 0, approval_ids: [], batchable: true };
      g.count++; g.approval_ids.push(it.id);
      if (it.high_risk || it.risk !== "low") { g.batchable = false; g.reason = "risk"; }
      groups.set(key, g);
    }
    const multi = Array.from(groups.values()).filter((g) => g.count >= 2);
    for (const it of items) it.group_key = multi.some((g) => g.key === `${it.project_id}|${it.action}`) ? `${it.project_id}|${it.action}` : undefined;
    // History: 40 human decisions on "draft_reply" in the last 30 days, 39 approved (97.5 %) -> one suggestion.
    const suggestions: InboxSuggestion[] = [{ kind: "raise_notify_threshold", action: "draft_reply", decided: 40, approved: 39, rate: 0.975, window_days: 30, max_risk: "low" }];
    return { generated_at: new Date().toISOString(), items, groups: multi, suggestions, rules: { suggest_window_days: 30, suggest_min_decisions: 20, suggest_rate: 0.95, undo: "client_side" } };
  }

  handle(method: string, seg: string[], body: any, fallback: () => Promise<unknown>): unknown {
    if (method === "GET" && seg[1] === "inbox") return this.inbox();
    if (seg[1] === "batch") {
      const pid = String(body?.filter?.project_id ?? ""), action = String(body?.filter?.action ?? "");
      const group = this.items().filter((i) => i.project_id === pid && i.action === action);
      if (!group.length || !group.every((i) => this.isDemo(i.id))) return fallback();
      if (group.length !== Number(body?.expected_count)) throw new Error("POST /approvals/batch -> 409");
      if (group.some((i) => i.high_risk) && !body?.include_high) throw new Error("POST /approvals/batch -> 409");
      const d = body?.decision === "approve" ? "approved" : "rejected";
      group.forEach((i) => this.decided.set(i.id, d));
      return { ok: true, count: group.length, decided: group.length };
    }
    const id = seg[1];
    if (seg[2] === "counterargument") {
      if (method === "POST") this.counters.add(id);
      else if (!this.counters.has(id) && !DEMO.find((d) => d.id === id)?.counter) throw new Error(`GET /approvals/${id}/counterargument -> 404`);
      return this.counter(id);
    }
    if (this.decided.has(id)) throw new Error(`POST /approvals/${id}/decision -> 409`);
    this.decided.set(id, body?.decision === "approve" ? "approved" : "rejected");
    return { ok: true };
  }
}
