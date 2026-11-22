import type { FieldChange, RestoreStep, VersionSummary } from "./agentHistoryApi";

/**
 * The agent's configuration history in plain words (F4, docs/architecture/agent-history.md). Pure (no React): the
 * HistorySection renders what these functions decide. `t` is the translator (key, params) -> text.
 */

export type T = (key: string, params?: Record<string, string | number>) => string;

/** Fields in display order; anything else is shown by its name. */
export const HISTORY_FIELDS = ["instructions", "creativity", "tags", "tone", "autonomy", "permissions", "skills", "budget_usd",
  "paused", "read_only", "rehearsal", "tools", "job", "access", "folders", "name", "title", "role", "department"] as const;

const KNOWN = new Set<string>(HISTORY_FIELDS);

/** The label of a field ("las instrucciones"). A secret field ("secret:x") is always just "a secret setting". */
export function fieldLabel(field: string, t: T): string {
  if (field.startsWith("secret:")) return t("hist.field.secret");
  return KNOWN.has(field) ? t(`hist.field.${field}`) : field;
}

/** "las instrucciones", "las instrucciones y el tono", "las instrucciones, el tono y 2 cosas más". */
export function whatChanged(fields: string[], t: T): string {
  const labels = fields.map((f) => fieldLabel(f, t));
  if (labels.length === 0) return t("hist.field.nothing");
  if (labels.length === 1) return labels[0];
  if (labels.length === 2) return t("hist.join.two", { a: labels[0], b: labels[1] });
  return t("hist.join.more", { a: labels[0], b: labels[1], n: labels.length - 2 });
}

/** The sentence of one line of the timeline. */
export function headline(v: VersionSummary, t: T): string {
  const who = v.actor_name?.trim() || t("hist.someone");
  const what = whatChanged(v.fields, t);
  if (v.source === "baseline") return t("hist.line.baseline");
  if (v.source === "agent.created") return t("hist.line.created");
  if (v.restored_from) return t("hist.line.restored", { who, n: v.restored_from });
  if (v.actor_kind === "system") {
    if (v.source.startsWith("trust.auto") && v.fields.includes("autonomy")) return t("hist.line.system_demoted");
    if (v.source.startsWith("trust.")) return t("hist.line.system_trust", { what });
    return t("hist.line.system", { what });
  }
  return t("hist.line.person", { who, what });
}

const str = (v: unknown) => (typeof v === "string" ? v : v == null ? "" : String(v));

function clip(s: string, n = 400): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

interface Perms { exceptions?: { category: string; mode: string; if?: { per_day_over?: number; amount_over?: number; new_recipient?: boolean } }[]; floor_lifted?: string[] }

function permsText(p: Perms | null | undefined, t: T): string {
  if (!p) return t("hist.value.perms_none");
  const parts: string[] = [];
  for (const e of p.exceptions ?? []) parts.push(t("hist.value.exception", { cat: t(`perm.cat.${e.category}`), mode: t(`perm.mode.${e.mode}`) }));
  for (const c of p.floor_lifted ?? []) parts.push(t("hist.value.floor_lifted", { cat: t(`perm.cat.${c}`) }));
  return parts.length ? parts.join(" · ") : t("hist.value.perms_default");
}

/** A value of a field in words, for "before" and "after". */
export function fieldValue(field: string, raw: unknown, t: T): string {
  if (field.startsWith("secret:")) return t("hist.value.hidden");
  switch (field) {
    case "instructions":
      return str(raw).trim() ? clip(str(raw)) : t("hist.value.empty");
    case "creativity":
      return t(`hist.creativity.${str(raw) || "medium"}`);
    case "autonomy":
      return str(raw) ? t(`hist.autonomy.${str(raw)}`) : t("hist.value.empty");
    case "tone":
      return str(raw) ? t(`hist.tone.${str(raw)}`) : t("hist.tone.default");
    case "budget_usd":
      return typeof raw === "number" && raw > 0 ? `US$ ${raw.toLocaleString("es", { maximumFractionDigits: 2 })}` : t("hist.value.no_cap");
    case "paused": case "read_only": case "rehearsal":
      return raw ? t("hist.value.on") : t("hist.value.off");
    case "permissions":
      return permsText(raw as Perms | null, t);
    case "skills": {
      const l = Array.isArray(raw) ? raw.map((s: any) => str(s?.name || s?.id)).filter(Boolean) : [];
      return l.length ? l.join(", ") : t("hist.value.none");
    }
    case "job": {
      const j = (raw ?? {}) as { responsibilities?: string[] };
      const l = j.responsibilities ?? [];
      return l.length ? clip(l.join(" · ")) : t("hist.value.none");
    }
    case "access": {
      const a = (raw ?? {}) as { visibility?: string; shares?: string[] };
      return a.visibility ? t("hist.value.access", { vis: t(`hist.visibility.${a.visibility}`), n: (a.shares ?? []).length }) : t("hist.value.none");
    }
    case "folders": {
      const l = Array.isArray(raw) ? raw.map((f: any) => `${str(f?.path)} (${str(f?.level)})`) : [];
      return l.length ? l.join(", ") : t("hist.value.none");
    }
  }
  if (Array.isArray(raw)) return raw.length ? raw.map(str).join(", ") : t("hist.value.none");
  if (raw && typeof raw === "object") return clip(JSON.stringify(raw));
  return str(raw) || t("hist.value.empty");
}

/** The before/after rows of a version, in display order. */
export function changeRows(changes: FieldChange[], t: T): { field: string; label: string; before: string; after: string; secret: boolean }[] {
  const order = (f: string) => { const i = (HISTORY_FIELDS as readonly string[]).indexOf(f); return i < 0 ? 999 : i; };
  return [...changes]
    .sort((a, b) => order(a.field) - order(b.field))
    .map((c) => ({ field: c.field, label: fieldLabel(c.field, t), before: fieldValue(c.field, c.from, t), after: fieldValue(c.field, c.to, t), secret: !!c.secret }));
}

/** Reasons the server gives for what is not restored. */
export const SKIP_REASONS = ["autonomy_earned", "permissions_freer", "skill_unavailable", "budget_above_ceiling", "tools_freer",
  "control", "structural", "access", "secret", "not_allowed", "unavailable", "failed"] as const;

/** One sentence per thing that will not (or did not) come back, grouped by field, the safety reasons first. */
export function skipLines(skips: RestoreStep[], t: T): { key: string; text: string; reason: string }[] {
  const prio = (r = "") => { const i = (SKIP_REASONS as readonly string[]).indexOf(r); return i < 0 ? 99 : i; };
  const seen = new Set<string>();
  const out: { key: string; text: string; reason: string }[] = [];
  for (const s of [...skips].sort((a, b) => prio(a.reason) - prio(b.reason))) {
    const reason = (SKIP_REASONS as readonly string[]).includes(s.reason ?? "") ? s.reason! : "failed";
    const what = s.skill ? t("hist.skip.skill_named", { name: s.skill.name || s.skill.id })
      : s.category ? t("hist.skip.category", { cat: t(`perm.cat.${s.category}`) }) : fieldLabel(s.field, t);
    const key = `${s.field}:${s.category ?? ""}:${s.skill?.id ?? ""}:${reason}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ key, reason, text: t("hist.skip.line", { what, why: t(`hist.reason.${reason}`) }) });
  }
  return out;
}

/** The distinct fields a restore will change ("las instrucciones y el tope de gasto"). */
export function applyFields(apply: RestoreStep[]): string[] {
  const out: string[] = [];
  for (const s of apply) if (!out.includes(s.field)) out.push(s.field);
  return out;
}

/** A version can be restored when it is not the current one and the person may restore. */
export function canRestoreVersion(v: VersionSummary, current: number, canRestore: boolean): boolean {
  return canRestore && v.version !== current;
}
