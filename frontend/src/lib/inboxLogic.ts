/** Approvals in one minute (B17, docs/architecture/approvals-inbox.md). Contract of GET /approvals/inbox and the
 * pure rules of the fast inbox: keys, swipe, when a quick approval needs an explicit confirm, batch eligibility. */
import type { Approval } from "./types";

export interface InboxCounter { summary: string; risks: string[] }
export interface InboxItem extends Approval {
  task_title?: string; request_id?: string; task_cost_usd: number; project_name?: string; group_key?: string;
  counterargument?: InboxCounter | null; high_risk: boolean;
}
export interface InboxGroup { key: string; project_id: string; project_name?: string; action: string; count: number; approval_ids: string[]; batchable: boolean; reason?: string }
export interface InboxSuggestion { kind: "raise_notify_threshold"; action: string; decided: number; approved: number; rate: number; window_days: number; max_risk: string }
export interface InboxRules { suggest_window_days: number; suggest_min_decisions: number; suggest_rate: number; undo: "client_side" }
export interface ApprovalInbox { generated_at: string; items: InboxItem[]; groups: InboxGroup[]; suggestions: InboxSuggestion[]; rules: InboxRules }

export type Command = "approve" | "reject" | "next" | "prev" | "expand";

/** A approve, R reject, J next, K prev, E expand (arrows also move). Modifier combos are left to the browser. */
export function keyToCommand(e: { key: string; ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean }): Command | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  switch (e.key.toLowerCase()) {
    case "a": return "approve";
    case "r": return "reject";
    case "j": case "arrowdown": return "next";
    case "k": case "arrowup": return "prev";
    case "e": return "expand";
  }
  return null;
}

/** Horizontal swipe: right approves, left rejects, past the threshold and mostly horizontal. */
export function swipeIntent(dx: number, dy = 0, threshold = 96): "approve" | "reject" | null {
  if (Math.abs(dx) < threshold || Math.abs(dy) > Math.abs(dx)) return null;
  return dx > 0 ? "approve" : "reject";
}

/** A quick approval (key A, swipe right) of a high-risk item always goes through an explicit confirm. */
export const needsConfirm = (it: Pick<InboxItem, "risk" | "high_risk">, decision: "approve" | "reject") =>
  decision === "approve" && (it.high_risk || it.risk === "high");

/** The batch group an item can be decided with ("approve all N"), only where POST /approvals/batch allows it. */
export function batchGroupOf(it: Pick<InboxItem, "group_key">, groups: InboxGroup[]): InboxGroup | null {
  if (!it.group_key) return null;
  const g = groups.find((x) => x.key === it.group_key);
  return g && g.batchable && g.count >= 2 ? g : null;
}

/** The deck the user sees: pending items not yet decided (or held for undo) on this device. */
export const deckOf = (items: InboxItem[], hidden: ReadonlySet<string>) => items.filter((i) => i.status === "pending" && !hidden.has(i.id));

export const clampIndex = (i: number, n: number) => (n <= 0 ? 0 : Math.min(Math.max(i, 0), n - 1));

/** Undo window of a decision before it is sent (client side: the API has no undo). */
export const UNDO_MS = 5000;
