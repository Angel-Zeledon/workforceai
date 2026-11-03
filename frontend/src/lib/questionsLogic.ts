// "Necesita tu ayuda": pure rules of the Pendientes tray and inbox (tested by questionsLogic.test.mjs).
// No imports with path aliases: node runs the test on this file directly.

export type QuestionStatus = "open" | "answered" | "reassigned" | "skipped" | "cancelled";
export type AnswerKind = "text" | "option" | "file" | "reassign" | "skip";

/** One question an agent asked a person (GET /questions item). */
export interface QuestionItem {
  id: string; task_id: string; request_id: string; agent_id: string;
  project_id?: string; group_key?: string;
  kind: "data" | "file" | "decision" | "contradiction" | "other";
  what_happened: string; need: string; options: string[];
  status: QuestionStatus; asked_for?: string;
  created_at: string; resolved_at: string | null; resolved_by?: string;
  answer_kind?: AnswerKind; answer?: string; file_path?: string; reassign_to?: string;
  reminders: number;
  task_title?: string; project_name?: string; batch_name?: string; waiting_seconds: number; for_me: boolean; can_answer: boolean;
}

export interface SkipImpact {
  task?: { id: string; title: string };
  skipped: { id: string; title: string }[];
  objectives_lost: { id: string; title: string }[];
  objectives: number; all_lost: boolean; allowed: boolean;
}

export interface QuestionGroup { key: string; kind: "batch" | "project" | "request"; label: string; items: QuestionItem[] }

const isOpen = (q: QuestionItem) => q.status === "open";

export function pendingCount(qs: QuestionItem[]): number {
  return qs.filter(isOpen).length;
}

/** Open questions grouped by batch / project / request; newest group first, oldest question first inside. */
export function groupQuestions(qs: QuestionItem[]): QuestionGroup[] {
  const by = new Map<string, QuestionGroup>();
  const newest = new Map<string, number>();
  for (const q of qs.filter(isOpen)) {
    const key = q.group_key || `request:${q.request_id}`;
    const kind = key.startsWith("batch:") ? "batch" : key.startsWith("project:") ? "project" : "request";
    let g = by.get(key);
    if (!g) {
      g = { key, kind, label: q.batch_name || q.project_name || q.task_title || "", items: [] };
      by.set(key, g);
    }
    if (!g.label && (q.project_name || q.task_title)) g.label = q.project_name || q.task_title || "";
    g.items.push(q);
    newest.set(key, Math.max(newest.get(key) ?? 0, +new Date(q.created_at)));
  }
  const groups = [...by.values()];
  for (const g of groups) g.items.sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at));
  return groups.sort((a, b) => (newest.get(b.key) ?? 0) - (newest.get(a.key) ?? 0));
}

/** How long a question waits, as a unit and a count for the i18n string. */
export function waitingParts(seconds: number): { unit: "now" | "min" | "h" | "d"; n: number } {
  const s = Math.max(0, Math.floor(seconds || 0));
  if (s < 60) return { unit: "now", n: 0 };
  if (s < 3600) return { unit: "min", n: Math.floor(s / 60) };
  if (s < 86400) return { unit: "h", n: Math.floor(s / 3600) };
  return { unit: "d", n: Math.floor(s / 86400) };
}

/** What the skip button leads to: refuse (nothing would be delivered), partial (some objectives lost), confirm. */
export function skipVerdict(imp: SkipImpact | null | undefined): "refuse" | "partial" | "confirm" | "unknown" {
  if (!imp) return "unknown";
  if (!imp.allowed || imp.all_lost) return "refuse";
  if (imp.objectives_lost.length > 0) return "partial";
  return "confirm";
}

/** Whether several selected questions can get one answer, and the options they all share. */
export function batchable(sel: QuestionItem[]): { ok: boolean; options: string[] } {
  if (!sel.length || sel.some((q) => !q.can_answer || !isOpen(q))) return { ok: false, options: [] };
  const [first, ...rest] = sel;
  const options = first.options.filter((o) => rest.every((q) => q.options.includes(o)));
  return { ok: true, options };
}
