/** Pure helpers of the rehearsal report (B9, docs/architecture/rehearsal.md). No React, no network. */

export interface CountableAction { verdict: string; would_decision: string }

/** Tally of the human verdicts (the same numbers the backend returns as `summary`). */
export function tally(actions: CountableAction[]) {
  const c = { total: actions.length, would_approve: 0, would_reject: 0, undecided: 0, would_need_approval: 0, would_be_denied: 0 };
  for (const a of actions) {
    if (a.verdict === "would_approve") c.would_approve++;
    else if (a.verdict === "would_reject") c.would_reject++;
    else c.undecided++;
    if (a.would_decision === "needs_approval") c.would_need_approval++;
    if (a.would_decision === "denied") c.would_be_denied++;
  }
  return c;
}

/** Share of decided entries the person would approve (0..1), or null when nothing was decided yet. */
export function approvalRate(c: { would_approve: number; would_reject: number }): number | null {
  const n = c.would_approve + c.would_reject;
  return n === 0 ? null : c.would_approve / n;
}

/** Preview rows in a stable order: recipients and money first, then the rest alphabetically, the "…" marker last. */
export function previewRows(preview: Record<string, string> | null | undefined): [string, string][] {
  const first = ["to", "cc", "bcc", "recipient", "recipients", "amount", "currency", "account", "subject", "title"];
  const rows = Object.entries(preview || {});
  const rank = (k: string) => (k === "…" ? 1000 : first.includes(k) ? first.indexOf(k) : 100);
  return rows.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b));
}

/** Clicking the verdict that is already set clears it. */
export function nextVerdict(current: string, clicked: "would_approve" | "would_reject"): "" | "would_approve" | "would_reject" {
  return current === clicked ? "" : clicked;
}
