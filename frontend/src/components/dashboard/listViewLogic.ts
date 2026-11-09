/**
 * The Dashboard as the Office's "Vista lista" (docs/superpowers/specs/2026-11-03-ux-simplificacion-propuesta.md, 5.2
 * point 2 and row 3.3; tanda 3, task G). Pure (no React): Dashboard.tsx uses it to know which sections it still owns,
 * which ones became links to their own screen, and where an old link (`/dashboard?section=customers`, `?tab=files`…)
 * should land now.
 */

/** What the list view still owns, in its own order. Informes stay here: they have no screen of their own. */
export const LIST_SECTIONS = ["overview", "agents", "tasks", "quality", "costs", "reports"] as const;
export type ListSection = (typeof LIST_SECTIONS)[number];

/** What has its own screen now: shown as a link ("Ir a") with its number, never as a section. */
export const GO_TO = [
  { id: "customers", href: "/clients", icon: "briefcase" },
  { id: "requests", href: "/chats", icon: "chat" },
  { id: "approvals", href: "/approvals/inbox", icon: "bell" },
  { id: "files", href: "/files", icon: "folder" },
] as const;
export type GoToId = (typeof GO_TO)[number]["id"];

/** Where a link lands: another screen, or a section of the list view (optionally a block inside it to scroll to). */
export type ListTarget =
  | { kind: "route"; href: string }
  | { kind: "section"; section: ListSection; anchor: string | null };

// Old section ids (the `dash-nav-*` of the 11 sections) and handy aliases in es/en/pt.
const ROUTES: Record<string, string> = {
  customers: "/clients", clients: "/clients", clientes: "/clients", district: "/clients",
  requests: "/chats", request: "/chats", pedidos: "/chats", pedido: "/chats", plan: "/chats", chats: "/chats",
  approvals: "/approvals/inbox", approval: "/approvals/inbox", pending: "/approvals/inbox", pendientes: "/approvals/inbox",
  aprobaciones: "/approvals/inbox", "aprovações": "/approvals/inbox", inbox: "/approvals/inbox",
  files: "/files", archivos: "/files", arquivos: "/files", artifacts: "/files", library: "/files",
};
const SECTIONS: Record<string, [ListSection, string | null]> = {
  overview: ["overview", null], summary: ["overview", null], resumen: ["overview", null], resumo: ["overview", null], kpis: ["overview", null],
  // the decided approvals (the old "Historial" of Aprobaciones) stay in Resumen
  history: ["overview", "decisions"], decisions: ["overview", "decisions"], decided: ["overview", "decisions"],
  agents: ["agents", null], agentes: ["agents", null], team: ["agents", null], equipo: ["agents", null],
  // conversations between agents now live under Agentes
  conversations: ["agents", "conversations"], conversaciones: ["agents", "conversations"], conversas: ["agents", "conversations"],
  tasks: ["tasks", null], task: ["tasks", null], tareas: ["tasks", null], tarefas: ["tasks", null],
  quality: ["quality", null], calidad: ["quality", null], qualidade: ["quality", null],
  costs: ["costs", null], cost: ["costs", null], costos: ["costs", null], custos: ["costs", null], errors: ["costs", null], budget: ["costs", null],
  reports: ["reports", null], report: ["reports", null], informes: ["reports", null], reportes: ["reports", null], "relatórios": ["reports", null],
};

/** The section name an address asks for: `?section=`, else `?tab=`, else `?s=` (the names used across the app). */
export function requestedSection(search: string | URLSearchParams | null | undefined): string {
  const q = typeof search === "string" || search == null ? new URLSearchParams(search ?? "") : search;
  return String(q.get("section") ?? q.get("tab") ?? q.get("s") ?? "").trim().toLowerCase();
}

/**
 * Translates a section name (old or new, any alias) to where it lives now. Unknown or empty opens Resumen, so an old
 * link never shows an empty page.
 */
export function listTarget(name: string | null | undefined): ListTarget {
  const k = String(name ?? "").trim().toLowerCase();
  if (ROUTES[k]) return { kind: "route", href: ROUTES[k] };
  const s = SECTIONS[k];
  if (s) return { kind: "section", section: s[0], anchor: s[1] };
  return { kind: "section", section: "overview", anchor: null };
}

/** Where `/dashboard?…` lands. */
export const targetOfSearch = (search: string | URLSearchParams | null | undefined): ListTarget => listTarget(requestedSection(search));

/** The address of a section of the list view (Resumen is the bare `/dashboard`), so the open section can be shared. */
export const sectionHref = (s: ListSection): string => (s === "overview" ? "/dashboard" : `/dashboard?section=${s}`);
