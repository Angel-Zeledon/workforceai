// Pure logic for the V2 agent profile / thought audit / intervention UI (no React, no path aliases: unit-testable with node --test).

export type Creativity = "low" | "medium" | "high";
export const CREATIVITY_LEVELS: Creativity[] = ["low", "medium", "high"];

export interface ProfileLimits { instructions_max: number; skills_max: number; skill_max_len: number }
export const DEFAULT_LIMITS: ProfileLimits = { instructions_max: 4000, skills_max: 20, skill_max_len: 40 };

export interface ProfileDraft { instructions: string; creativity: Creativity; skills: string[]; tone: string }

export type ProfileIssue =
  | { code: "instructions_too_long"; max: number }
  | { code: "too_many_skills"; max: number }
  | { code: "skill_too_long"; max: number; skill: string }
  | { code: "looks_like_secret" };

/** Heuristic client-side secret detector (the backend is authoritative; this only warns early). */
const SECRET_PATTERNS: RegExp[] = [
  /\bsk-[A-Za-z0-9_-]{16,}/,
  /\bAKIA[0-9A-Z]{16}\b/,
  /\bgh[pousr]_[A-Za-z0-9]{20,}/,
  /\bxox[abprs]-[A-Za-z0-9-]{10,}/,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  /\bBearer\s+[A-Za-z0-9._~+/=-]{20,}/i,
  /\b(?:password|passwd|secret|api[_-]?key|token)\s*[:=]\s*\S{6,}/i,
];
export function looksLikeSecret(text: string): boolean {
  return SECRET_PATTERNS.some((re) => re.test(text));
}

export function normalizeSkill(s: string): string {
  return s.trim().replace(/\s+/g, " ");
}

/** Adds a tag when valid and not a (case-insensitive) duplicate; returns the same array otherwise. */
export function addSkill(skills: string[], raw: string, limits: ProfileLimits = DEFAULT_LIMITS): string[] {
  const s = normalizeSkill(raw);
  if (!s || s.length > limits.skill_max_len || skills.length >= limits.skills_max) return skills;
  if (skills.some((x) => x.toLowerCase() === s.toLowerCase())) return skills;
  return [...skills, s];
}
export const removeSkill = (skills: string[], skill: string): string[] => skills.filter((x) => x !== skill);

export function validateProfile(d: ProfileDraft, limits: ProfileLimits = DEFAULT_LIMITS): ProfileIssue[] {
  const out: ProfileIssue[] = [];
  if ([...d.instructions].length > limits.instructions_max) out.push({ code: "instructions_too_long", max: limits.instructions_max });
  if (d.skills.length > limits.skills_max) out.push({ code: "too_many_skills", max: limits.skills_max });
  for (const s of d.skills) if ([...s].length > limits.skill_max_len) out.push({ code: "skill_too_long", max: limits.skill_max_len, skill: s });
  if (looksLikeSecret(d.instructions)) out.push({ code: "looks_like_secret" });
  return out;
}

export function sameSkills(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}
export function isDirty(base: ProfileDraft, d: ProfileDraft): boolean {
  return base.instructions !== d.instructions || base.creativity !== d.creativity || base.tone !== d.tone || !sameSkills(base.skills, d.skills);
}

export interface FieldDiff { from: unknown; to: unknown }
export interface DiffRow { field: string; kind: "text" | "list" | "scalar"; from: string; to: string; added?: string[]; removed?: string[] }

const clip = (s: string, n = 60) => (s.length > n ? `${s.slice(0, n)}...` : s);

/** Turns a version's `diff` object into display rows (stable field order). */
export function diffRows(diff: Record<string, FieldDiff> | null | undefined): DiffRow[] {
  const order = ["instructions", "creativity", "tone", "skills"];
  const keys = Object.keys(diff ?? {}).sort((a, b) => (order.indexOf(a) < 0 ? 99 : order.indexOf(a)) - (order.indexOf(b) < 0 ? 99 : order.indexOf(b)));
  return keys.map((field) => {
    const { from, to } = (diff as Record<string, FieldDiff>)[field];
    if (Array.isArray(from) || Array.isArray(to)) {
      const f = (Array.isArray(from) ? from : []).map(String), t = (Array.isArray(to) ? to : []).map(String);
      return { field, kind: "list" as const, from: f.join(", "), to: t.join(", "), added: t.filter((x) => !f.includes(x)), removed: f.filter((x) => !t.includes(x)) };
    }
    const kind = field === "instructions" ? "text" : "scalar";
    const fs = from == null ? "" : String(from), ts = to == null ? "" : String(to);
    return { field, kind, from: kind === "text" ? clip(fs) : fs, to: kind === "text" ? clip(ts) : ts };
  });
}

/** HTTP status embedded in the api client's error message ("PUT /x -> 403"), or 0. */
export function errorStatus(e: unknown): number {
  const m = /->\s*(\d{3})\s*$/.exec(e instanceof Error ? e.message : String(e));
  return m ? Number(m[1]) : 0;
}

// ---- thought audit ----
export interface PromptSegment { kind: "text" | "untrusted"; label: string; text: string; truncated: boolean }

/** Untrusted (external) segments start collapsed; so does everything long enough to hide the rest of the prompt. */
export function segmentDefaultOpen(seg: Pick<PromptSegment, "kind">): boolean {
  return seg.kind !== "untrusted";
}

export function traceTotalTokens(t: { input_tokens: number; output_tokens: number }): number {
  return (t.input_tokens || 0) + (t.output_tokens || 0);
}
export function fmtLatency(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`;
}

/** Cursor for the next page of traces (the oldest loaded one), or "" when the page was not full. */
export function nextBefore(traces: { created_at: string }[], limit: number): string {
  return traces.length >= limit ? traces[traces.length - 1].created_at : "";
}

// ---- intervention ----
export const INTERVENABLE = new Set(["pending", "running", "blocked", "awaiting_approval"]);
export const canIntervene = (status: string) => INTERVENABLE.has(status);

export const STEER_NOTE_MAX = 2000;
export function steerNoteError(note: string): "empty" | "too_long" | "" {
  const n = note.trim();
  if (!n) return "empty";
  return [...n].length > STEER_NOTE_MAX ? "too_long" : "";
}
/** apply_now re-runs the task: it must be offered only when the backend says so, and only after an explicit confirmation. */
export function canSendApplyNow(i: { can_steer: boolean; can_apply_now: boolean }, note: string, confirmed: boolean): boolean {
  return i.can_steer && i.can_apply_now && steerNoteError(note) === "" && confirmed;
}
export function fmtEstimate(usd: number): string {
  const v = Number.isFinite(usd) ? usd : 0;
  return `$${v < 0.1 ? v.toFixed(3) : v.toFixed(2)}`;
}
