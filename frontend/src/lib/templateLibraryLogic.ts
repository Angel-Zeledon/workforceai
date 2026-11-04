/**
 * One template library in Proyectos (UX tanda 2): it shows side by side the quick-request templates (workflow
 * templates, GET /workflow-templates, launched as one request) and the project templates (GET /project-templates,
 * launched as a project). Two sources that are never merged: this only decides what is shown.
 */
export const LIBRARY_KINDS = ["all", "quick", "project"] as const;
export type LibraryKind = (typeof LIBRARY_KINDS)[number];

/** `?kind=` -> the filter it opens with (anything else shows both). */
export function kindFromParam(raw?: string | null): LibraryKind {
  const k = (raw ?? "").trim().toLowerCase();
  if (k === "quick" || k === "workflow" || k === "workflows" || k === "request") return "quick";
  if (k === "project" || k === "projects") return "project";
  return "all";
}

/** Which of the two groups the filter shows. */
export function showsKind(filter: LibraryKind, kind: "quick" | "project"): boolean {
  return filter === "all" || filter === kind;
}

/** One list of categories for the two sources: the project order first, then whatever else the quick ones use. */
export function libraryCategories(order: readonly string[], projectCats: string[], quickCats: string[]): string[] {
  const present = new Set([...projectCats, ...quickCats].map((c) => c || "general"));
  const known = order.filter((c) => present.has(c));
  const extra = [...present].filter((c) => !order.includes(c)).sort();
  return [...known, ...extra];
}

/** The same search for both: every word must appear in the name or the description (accents and case ignored). */
export function matchesSearch(text: string, query: string): boolean {
  const fold = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = fold(text);
  return words.every((w) => hay.includes(w));
}
