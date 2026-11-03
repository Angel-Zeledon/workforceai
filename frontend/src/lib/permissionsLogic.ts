/** Pure helpers of the approval permissions UI (docs/architecture/06-permisos-autonomia.md section 11). No React, no network. */

export const PLAIN_LEVELS = ["ask_all", "important", "alone"] as const;
export type PlainLevel = (typeof PLAIN_LEVELS)[number] | "suggest";
export const CATEGORIES = ["messages", "publish", "payments", "delete", "contracts", "spend"] as const;
export type Category = (typeof CATEGORIES)[number];
export const FLOOR: readonly string[] = ["payments", "contracts", "delete"];
export type Mode = "ask" | "never" | "ask_if";

export interface AskIf { per_day_over?: number; amount_over?: number; new_recipient?: boolean }

export function isFloor(cat: string): boolean { return FLOOR.includes(cat); }

/** Does the level picked apply exceptions? "Asks for everything" (and suggest) ask anyway. */
export function exceptionsApply(level: string): boolean { return level === "important" || level === "alone"; }

/** An "ask if…" body is valid when at least one condition is set and numbers are sane. */
export function validAskIf(c: AskIf | undefined | null): boolean {
  if (!c) return false;
  const day = c.per_day_over ?? 0, amt = c.amount_over;
  if (day < 0 || day > 10000 || !Number.isInteger(day)) return false;
  if (amt !== undefined && (!Number.isFinite(amt) || amt < 0)) return false;
  return day > 0 || amt !== undefined || !!c.new_recipient;
}

/** Why an approval was asked for, and how to avoid it next time: i18n keys plus the category when a rule names one. */
export interface Reason { why: string; avoid: string; category?: string; rememberable: boolean }

const catOf = (rule: string) => rule.slice(rule.lastIndexOf(":") + 1);

/**
 * Maps the rule that asked (Approval.policy_rule) and, for connection-backed actions, the gateway's flags to the
 * explanation shown on the approval card. Rules from the agent's own permissions can be remembered ("approve and do
 * not ask again"); rules of the organization or the safety floor cannot.
 */
export function approvalReason(rule?: string | null, flags?: readonly string[] | null): Reason | null {
  const r = (rule ?? "").trim();
  const f = flags ?? [];
  const p = (pre: string) => r.startsWith(pre);
  if (p("perm.important:")) return { why: "perm.why.important", avoid: "perm.avoid.important", category: catOf(r), rememberable: catOf(r) !== "other" };
  if (p("perm.floor:")) return { why: "perm.why.floor", avoid: "perm.avoid.floor", category: catOf(r), rememberable: false };
  if (p("perm.ask:")) return { why: "perm.why.ask", avoid: "perm.avoid.ask", category: catOf(r), rememberable: true };
  if (p("perm.if_amount:")) return { why: "perm.why.ifAmount", avoid: "perm.avoid.askIf", category: catOf(r), rememberable: true };
  if (p("perm.if_new_recipient:")) return { why: "perm.why.ifNew", avoid: "perm.avoid.askIf", category: catOf(r), rememberable: true };
  if (p("limit:perm.per_day:")) return { why: "perm.why.perDay", avoid: "perm.avoid.askIf", category: catOf(r), rememberable: true };
  if (r === "autonomy.approve_each" || p("baseline.autonomy:") || (!r && f.includes("autonomy")) || r === "native.autonomy")
    return { why: "perm.why.askAll", avoid: "perm.avoid.askAll", rememberable: false };
  if (p("baseline.approval_action:") || p("org.always_approve:")) return { why: "perm.why.alwaysList", avoid: "perm.avoid.alwaysList", rememberable: false };
  if (r === "baseline.risk_high" || r === "risk.high") return { why: "perm.why.riskHigh", avoid: "perm.avoid.none", rememberable: false };
  if (r === "org.amount" || p("org.amount_tier")) return { why: "perm.why.amount", avoid: "perm.avoid.orgRules", rememberable: false };
  if (r === "org.new_recipient") return { why: "perm.why.newRecipient", avoid: "perm.avoid.orgRules", rememberable: false };
  if (r === "org.categories") return { why: "perm.why.orgCategory", avoid: "perm.avoid.orgRules", rememberable: false };
  if (p("dual_approval")) return { why: "perm.why.dual", avoid: "perm.avoid.orgRules", rememberable: false };
  if (p("limit:")) return { why: "perm.why.limit", avoid: "perm.avoid.orgRules", rememberable: false };
  if (r === "amount.unparseable") return { why: "perm.why.amountUnreadable", avoid: "perm.avoid.none", rememberable: false };
  if (r === "policy.unavailable") return { why: "perm.why.unavailable", avoid: "perm.avoid.none", rememberable: false };
  if (f.includes("always_approval")) return { why: "perm.why.gatewayAlways", avoid: "perm.avoid.none", rememberable: false };
  if (f.some((x) => x === "tainted_task" || x === "injection_suspected" || x === "cross_connection" || x === "recipient_from_external_content"))
    return { why: "perm.why.tainted", avoid: "perm.avoid.none", rememberable: false };
  if (r) return { why: "perm.why.rule", avoid: "perm.avoid.none", rememberable: false };
  return null;
}

/** The short label of an exception as stored ("Asks above 500", "Never asks"). Returns an i18n key and its vars. */
export function exceptionLabel(mode: string, c?: AskIf | null): { key: string; vars?: Record<string, string | number> }[] {
  if (mode === "ask") return [{ key: "perm.mode.ask" }];
  if (mode === "never") return [{ key: "perm.mode.never" }];
  const out: { key: string; vars?: Record<string, string | number> }[] = [];
  if (c?.per_day_over) out.push({ key: "perm.if.perDayShort", vars: { n: c.per_day_over } });
  if (c?.amount_over !== undefined && c?.amount_over !== null) out.push({ key: "perm.if.amountShort", vars: { n: c.amount_over } });
  if (c?.new_recipient) out.push({ key: "perm.if.newShort" });
  return out.length ? out : [{ key: "perm.mode.ask_if" }];
}
