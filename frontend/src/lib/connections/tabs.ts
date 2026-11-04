/**
 * Conexiones has three tabs (UX tanda 2): Conexiones (accounts, data inboxes, link imports, databases), "Quién puede
 * usar qué" (permissions per agent + what each agent can do) and "Uso y límites". The six old tabs live inside them,
 * so an old link (`/connections?tab=matrix`) still lands on the right place: the new tab plus the part inside it.
 */
export const CONN_PAGE_TABS = ["connections", "access", "usage"] as const;
export type ConnPageTab = (typeof CONN_PAGE_TABS)[number];

/** Parts inside a tab: sections of the Conexiones page, the two views of "Quién puede usar qué", the two blocks of "Uso y límites". */
export type ConnSection = "accounts" | "inboxes" | "links" | "databases" | "matrix" | "capabilities" | "log" | "limits";

export interface ConnTarget { tab: ConnPageTab; section?: ConnSection }

const MAP: Record<string, ConnTarget> = {
  // new names
  connections: { tab: "connections" },
  access: { tab: "access" },
  usage: { tab: "usage" },
  // the parts of Conexiones
  accounts: { tab: "connections", section: "accounts" },
  inboxes: { tab: "connections", section: "inboxes" },
  inbox: { tab: "connections", section: "inboxes" },
  links: { tab: "connections", section: "links" },
  imports: { tab: "connections", section: "links" },
  "link-imports": { tab: "connections", section: "links" },
  databases: { tab: "connections", section: "databases" },
  database: { tab: "connections", section: "databases" },
  db: { tab: "connections", section: "databases" },
  // the old "Permisos por agente" and "Qué puede hacer cada agente"
  matrix: { tab: "access", section: "matrix" },
  grants: { tab: "access", section: "matrix" },
  permissions: { tab: "access", section: "matrix" },
  capabilities: { tab: "access", section: "capabilities" },
  // the old "Registro de uso" and "Límites"
  log: { tab: "usage", section: "log" },
  limits: { tab: "usage", section: "limits" },
};

/** `?tab=` value (old or new, any case) -> the tab that contains it. Anything unknown opens Conexiones. */
export function resolveConnTab(raw?: string | null): ConnTarget {
  const key = (raw ?? "").trim().toLowerCase();
  return Object.hasOwn(MAP, key) ? { ...MAP[key] } : { tab: "connections" };
}

/** The view shown inside "Quién puede usar qué": the one the link asked for, otherwise the permissions table. */
export function accessViewOf(section?: ConnSection): "matrix" | "capabilities" {
  return section === "capabilities" ? "capabilities" : "matrix";
}
