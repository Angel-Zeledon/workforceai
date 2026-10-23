/** Pure helpers of the visible shield (B10, docs/architecture/shield.md). No React, no network. */

/** One "external content tried to give an agent orders" signal. Ids only: never the third-party text. */
export interface ShieldAlert {
  id: string;
  agent_id: string;
  task_id: string;
  request_id: string;
  /** tool that read the content (email, calendar, fs, web, ...) and its action (search, read, fetch, ...) */
  tool: string;
  action: string;
  /** connection | native | web */
  source: string;
  ts: string;
}

const str = (v: unknown) => (typeof v === "string" ? v : "");

/** The realtime security.alert frame (kind prompt_injection_suspected) as an alert; null for any other frame. */
export function alertFromFrame(f: { id?: string; type: string; ts?: string; agent_id?: string | null; payload?: any }): ShieldAlert | null {
  if (f.type !== "security.alert") return null;
  const p = f.payload || {};
  if (p.kind !== "prompt_injection_suspected") return null;
  const agent = str(p.agent_id) || str(f.agent_id);
  if (!agent) return null;
  return {
    id: str(f.id) || `${agent}:${str(p.task_id)}:${str(f.ts)}`,
    agent_id: agent, task_id: str(p.task_id), request_id: str(p.request_id),
    tool: str(p.tool), action: str(p.action), source: str(p.source), ts: str(f.ts) || new Date().toISOString(),
  };
}

/** An audit record of GET /audit?type=security.injection_suspected as an alert. */
export function alertFromAudit(r: { id?: string; ts?: string; actor?: string; action?: string; request_id?: string; details?: any }): ShieldAlert | null {
  if (r.action !== "security.injection_suspected" || !r.actor) return null;
  const d = r.details && typeof r.details === "object" ? r.details : {};
  return {
    id: str(r.id) || `${r.actor}:${str(d.task_id)}:${str(r.ts)}`,
    agent_id: r.actor, task_id: str(d.task_id), request_id: str(r.request_id) || str(d.request_id),
    tool: str(d.tool), action: str(d.action), source: str(d.source), ts: str(r.ts),
  };
}

/** "email.search", "web.fetch", or just the tool when the action is unknown. */
export function sourceLabel(a: Pick<ShieldAlert, "tool" | "action">): string {
  if (!a.tool) return "?";
  return a.action ? `${a.tool}.${a.action}` : a.tool;
}

/** Adds alerts newest first, without duplicates, keeping at most max. */
export function mergeAlerts(list: ShieldAlert[], add: ShieldAlert[], max = 100): ShieldAlert[] {
  const seen = new Set<string>();
  const out: ShieldAlert[] = [];
  for (const a of [...add, ...list]) {
    if (seen.has(a.id)) continue;
    seen.add(a.id);
    out.push(a);
  }
  return out.sort((x, y) => +new Date(y.ts) - +new Date(x.ts)).slice(0, max);
}

/** Opacity of the shield over the desk: full while it pops, then fades. t in [0,1] of its life. */
export function shieldOpacity(t: number): number {
  if (t <= 0) return 0;
  if (t >= 1) return 0;
  if (t < 0.08) return t / 0.08;
  return t < 0.75 ? 1 : (1 - t) / 0.25;
}
