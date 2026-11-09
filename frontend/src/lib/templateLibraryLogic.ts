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

/** The source filter of the library: everything, only what comes with the app, or only the organization's own. */
export type LibrarySource = "all" | "builtin" | "org";

/**
 * Whether the quick-request group shows the one-line note "they come with the app, see them all" instead of
 * vanishing: the quick-request templates are all built in, so "Mías y de la organización" hides every one of them.
 * Only when that filter is the reason (the other filters would still leave some: `hiddenByOrigin` > 0) and the
 * kind filter still asks for quick requests (with "Proyecto" they are not wanted anyway).
 */
export function showQuickBuiltinNote(source: LibrarySource, kind: LibraryKind, hiddenByOrigin: number): boolean {
  return source === "org" && showsKind(kind, "quick") && hiddenByOrigin > 0;
}

/** Whether a filter or the search narrows the library (the empty state then offers to clear them). */
export function hasActiveFilters(f: { query: string; category: string; source: LibrarySource; kind: LibraryKind }): boolean {
  return f.query.trim() !== "" || f.category !== "all" || f.source !== "all" || f.kind !== "all";
}

/**
 * What the library says when the filters leave no card: nothing (`null`, when the quick-request note already
 * explains it all), "projects" (only the organization's own project templates were asked for and there are none
 * that match) or "generic" (no template matches the search or the filters).
 */
export function libraryEmptyState(f: { nothing: boolean; quickNote: boolean; source: LibrarySource; kind: LibraryKind }): "projects" | "generic" | null {
  if (!f.nothing) return null;
  if (f.quickNote && f.kind === "quick") return null;
  if (f.source === "org" && f.kind !== "quick") return "projects";
  return "generic";
}
