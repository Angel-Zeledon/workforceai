/** Approvals in one minute (B17, docs/architecture/approvals-inbox.md). Contract of GET /approvals/inbox and the
 * pure rules of the fast inbox: keys, swipe, when a quick approval needs an explicit confirm, batch eligibility. */
import type { Approval } from "./types";

export interface InboxCounter { summary: string; risks: string[] }
export interface InboxItem extends Approval {
  task_title?: string; request_id?: string; task_cost_usd: number; project_name?: string; group_key?: string;
  counterargument?: InboxCounter | null; high_risk: boolean;
  /** E0 "my approvals": the reader asked for it / may decide it now (absent on older servers) */
  requested_by_me?: boolean; can_decide?: boolean;
}
/** E0 filters of the inbox: everything, the ones I asked for, the ones I can decide. */
export type InboxMine = "" | "requested" | "decidable";
export interface InboxCounts { all: number; requested: number; decidable: number }
export interface InboxGroup { key: string; project_id: string; project_name?: string; action: string; count: number; approval_ids: string[]; batchable: boolean; reason?: string }
export interface InboxSuggestion { kind: "raise_notify_threshold"; action: string; decided: number; approved: number; rate: number; window_days: number; max_risk: string }
export interface InboxRules { suggest_window_days: number; suggest_min_decisions: number; suggest_rate: number; undo: "client_side" }
export interface ApprovalInbox { generated_at: string; items: InboxItem[]; groups: InboxGroup[]; suggestions: InboxSuggestion[]; rules: InboxRules; mine?: InboxMine; counts?: InboxCounts }

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

/**
 * The ONE number of Pendientes for the whole app (menu, office tray, dashboard, top bar): approvals waiting for a
 * decision plus questions agents asked that are still open. Everyone reads it from the same store.
 */
export function pendingTotal(approvals: Record<string, { status: string }> | { status: string }[], questions: Record<string, { status: string }> | { status: string }[]): number {
  const list = <T,>(x: Record<string, T> | T[]) => (Array.isArray(x) ? x : Object.values(x));
  return list(approvals).filter((a) => a.status === "pending").length + list(questions).filter((q) => q.status === "open").length;
}

/**
 * Filters of Pendientes. "email" is what Seguridad used to call "Correos para aprobar": the e-mail drafts waiting in the
 * outbox plus the approvals whose action sends or prepares an e-mail. One place for all decisions; Seguridad links here.
 */
export type InboxFilter = "" | "email";
export const INBOX_FILTERS: InboxFilter[] = ["", "email"];

/** `?filter=` of the address: anything unknown is "everything". */
export const parseInboxFilter = (v: string | null | undefined): InboxFilter => (String(v ?? "").trim().toLowerCase() === "email" ? "email" : "");

/** The address of Pendientes with a filter (`/approvals/inbox?filter=email`). */
export const inboxHref = (f: InboxFilter) => (f ? `/approvals/inbox?filter=${f}` : "/approvals/inbox");

const EMAIL_PREFIXES = ["email.", "mail.", "smtp."];
/** Providers that do more than e-mail (Outlook also has a calendar): only their sending or drafting actions count. */
const MAILBOX_PREFIXES = ["gmail.", "outlook.", "imap."];
const EMAIL_VERBS = new Set(["email_send", "send_email", "send_reply", "draft_reply", "reply_email", "send_mail"]);

/** True when the action sends or prepares an e-mail ("email.send", "gmail.send_reply", "draft_reply"). */
export function isEmailAction(code: string | null | undefined): boolean {
  const c = String(code ?? "").trim().toLowerCase();
  if (!c) return false;
  if (EMAIL_PREFIXES.some((p) => c.startsWith(p))) return true;
  if (MAILBOX_PREFIXES.some((p) => c.startsWith(p))) return /send|reply|draft|mail/.test(c.slice(c.indexOf(".") + 1));
  return EMAIL_VERBS.has(c.replace(/[^a-z0-9]+/g, "_")) || EMAIL_VERBS.has(c.split(".").pop()!.replace(/[^a-z0-9]+/g, "_"));
}

/** The approvals a filter shows (all of them without a filter). */
export const filterInbox = <T extends { action: string }>(items: T[], f: InboxFilter): T[] => (f === "email" ? items.filter((i) => isEmailAction(i.action)) : items);

/** Outbox drafts still waiting for a person (to edit, approve or cancel in the window) and the last few that ended. */
export const OUTBOX_WAITING = ["draft", "pending_approval", "held", "blocked"] as const;
export const OUTBOX_DONE = ["sent", "cancelled", "rejected"] as const;
export function emailDrafts<T extends { status: string }>(drafts: T[], recentDone = 4): { waiting: T[]; done: T[] } {
  return {
    waiting: drafts.filter((d) => (OUTBOX_WAITING as readonly string[]).includes(d.status)),
    done: drafts.filter((d) => (OUTBOX_DONE as readonly string[]).includes(d.status)).slice(0, recentDone),
  };
}
