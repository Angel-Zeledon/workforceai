import type { Initiative } from "../initiativeApi";
import type { NeedsYouItem, NeedsYouResult } from "../needsYouLogic";

/**
 * "Te necesitan" in mock mode (NEXT_PUBLIC_MOCK=true): the demo acts as its owner, like the backend without accounts,
 * so the list is the approvals of the mock inbox plus the pending suggestions, ordered as in
 * backend/internal/needsyou (urgency, then the oldest). The demo has no trust proposals (every agent is on probation)
 * and no agent questions.
 */
type InboxLike = { id: string; agent_id: string; action: string; title: string; risk: string; created_at: string; task_title?: string;
  project_name?: string; high_risk?: boolean; required_approvals?: number };

const DAY = 24 * 3600_000;
const rank = { high: 2, normal: 1, low: 0 } as const;

export function mockNeedsYou(approvals: InboxLike[], suggestions: Initiative[], agentName: (id: string) => string, now = Date.now(), countOnly = false): NeedsYouResult {
  const items: NeedsYouItem[] = [];
  for (const a of approvals) {
    const high = a.risk === "high" || !!a.high_risk || (a.required_approvals ?? 1) > 1;
    items.push({ kind: "approval", id: a.id, title: a.title || a.action, detail: a.task_title, agent_id: a.agent_id, agent_name: agentName(a.agent_id),
      created_at: a.created_at, risk: a.risk, double: (a.required_approvals ?? 1) > 1, action: a.action, project_name: a.project_name,
      urgency: high || now - Date.parse(a.created_at) >= DAY ? "high" : "normal", target: `/approvals/${encodeURIComponent(a.id)}`, direct: !high });
  }
  for (const s of suggestions) {
    if (s.status !== "pending") continue;
    items.push({ kind: "suggestion", id: s.id, title: s.title, detail: s.body, agent_id: s.agent_id, agent_name: agentName(s.agent_id),
      created_at: s.created_at, signal: s.signal, params: s.params, urgency: "low", target: "/dashboard", direct: true });
  }
  items.sort((x, y) => rank[y.urgency] - rank[x.urgency] || x.created_at.localeCompare(y.created_at) || x.id.localeCompare(y.id));
  const counts: Record<string, number> = {};
  for (const it of items) counts[it.kind] = (counts[it.kind] || 0) + 1;
  return { items: countOnly ? [] : items, total: items.length, counts };
}
