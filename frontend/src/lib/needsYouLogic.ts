/**
 * "Te necesitan" (docs/architecture/needs-you.md): pure rules of the list. Contract: backend/internal/api/needs_you.go.
 * The server decides what each person sees and may resolve; this file only shows it.
 */

export type NeedsYouKind = "approval" | "question" | "suggestion" | "trust";

export interface NeedsYouItem {
  /** approval | question | suggestion | trust, or a newer kind (shown with a generic label and its link). */
  kind: string;
  id: string;
  title: string;
  detail?: string;
  agent_id?: string;
  agent_name?: string;
  office_id?: string;
  office_name?: string;
  created_at: string;
  risk?: string;
  double?: boolean;
  urgency: "high" | "normal" | "low";
  /** Route of the screen where the item is resolved. */
  target: string;
  /** May be resolved from the list in one step. */
  direct: boolean;
  action?: string;
  project_name?: string;
  signal?: string;
  params?: Record<string, string>;
  options?: string[];
  proposal_kind?: "promotion" | "demotion" | string;
  from_level?: string;
  to_level?: string;
}

export interface NeedsYouResult {
  items: NeedsYouItem[];
  total: number;
  counts: Record<string, number>;
  truncated?: boolean;
  unavailable?: string[];
}

export const KIND_ORDER: NeedsYouKind[] = ["approval", "question", "suggestion", "trust"];

/** The existing live events that change what waits for a person (no channel of its own). */
const FRAMES = new Set([
  "approval.requested", "approval.resolved", "approval.progress",
  "question.asked", "question.resolved", "task.needs_input",
  "trust.proposal", "agent.autonomy_changed",
  "initiative.raised", "initiative.resolved",
]);
export const isNeedsYouFrame = (type: string) => FRAMES.has(type);

export type NeedsYouAction = "approve" | "reject" | "accept" | "dismiss";

/** What may be done from the list. Approving a high-risk approval, answering a question or deciding a trust
 * proposal needs their own screen: the list only links there. */
export function actionsOf(it: Pick<NeedsYouItem, "kind" | "direct">): NeedsYouAction[] {
  if (it.kind === "approval") return it.direct ? ["approve", "reject"] : ["reject"];
  if (it.kind === "suggestion" && it.direct) return ["accept", "dismiss"];
  return [];
}

export type TitleRef = { text: string } | { key: string; vars: Record<string, string> };

/** The title in plain words: the source's own text; a trust proposal composed in the viewer's language (the level is
 * an i18n key the caller translates). */
export function titleRef(it: Pick<NeedsYouItem, "kind" | "title" | "proposal_kind" | "agent_name" | "agent_id" | "to_level">): TitleRef {
  if (it.kind === "trust") {
    return { key: it.proposal_kind === "demotion" ? "needsYou.trust.demotion" : "needsYou.trust.promotion",
      vars: { agent: it.agent_name || it.agent_id || "", level: `autonomy.${it.to_level || ""}` } };
  }
  return it.title?.trim() ? { text: it.title } : { key: "needsYou.untitled", vars: {} };
}

/** The label key of a kind (unknown kinds share one). */
export const kindKey = (kind: string) => (KIND_ORDER.includes(kind as NeedsYouKind) ? `needsYou.kind.${kind}` : "needsYou.kind.other");

/** The tabs: everything, then each kind with something waiting (fixed order; newer kinds after). */
export function kindTabs(r: Pick<NeedsYouResult, "total" | "counts">): { kind: string; count: number }[] {
  const out = [{ kind: "", count: r.total }];
  const known = KIND_ORDER.filter((k) => (r.counts[k] || 0) > 0).map((k) => ({ kind: k as string, count: r.counts[k] }));
  const extra = Object.keys(r.counts).filter((k) => !KIND_ORDER.includes(k as NeedsYouKind) && r.counts[k] > 0).sort()
    .map((k) => ({ kind: k, count: r.counts[k] }));
  return [...out, ...known, ...extra];
}

export const filterKind = (items: NeedsYouItem[], kind: string) => (kind ? items.filter((i) => i.kind === kind) : items);

/** The list after the person resolved an item here (before the server's next answer). */
export function withoutItem(r: NeedsYouResult, kind: string, id: string): NeedsYouResult {
  if (!r.items.some((i) => i.kind === kind && i.id === id)) return r;
  return {
    ...r,
    items: r.items.filter((i) => !(i.kind === kind && i.id === id)),
    total: Math.max(0, r.total - 1),
    counts: { ...r.counts, [kind]: Math.max(0, (r.counts[kind] || 0) - 1) },
  };
}

export const badgeText = (n: number) => (n <= 0 ? "" : n > 99 ? "99+" : String(n));
