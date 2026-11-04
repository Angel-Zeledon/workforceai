/**
 * Bridge between plain code (zustand stores, handlers) and the Next.js router.
 * AppShell registers the router; `go()` navigates to a real route (browser history, deep links).
 */
let navigator: ((href: string) => void) | null = null;
export const setNavigator = (fn: ((href: string) => void) | null) => { navigator = fn; };
export function go(href: string) {
  if (navigator) navigator(href);
  else if (typeof window !== "undefined") window.location.assign(href);
}

/** Focus an agent and navigate to the office ("/"); the selection lives in the global store and survives the route change. */
export function openInOffice(agentId: string, select: (id: string) => void) {
  select(agentId);
  go("/");
}

/** Sections of the app: route, i18n label key and icon. Settings is a section too (bottom of the menu). */
export const SECTIONS = [
  { id: "office", href: "/", label: "nav.office", icon: "office" },
  { id: "pending", href: "/approvals/inbox", label: "nav.pending", icon: "bell" },
  { id: "chats", href: "/chats", label: "nav.chats", icon: "chat" },
  { id: "projects", href: "/projects", label: "nav.projects", icon: "flag" },
  // >>> org
  { id: "org", href: "/org", label: "nav.org", icon: "users" },
  // <<< org
  { id: "customers", href: "/clients", label: "nav.customers", icon: "briefcase" },
  { id: "drive", href: "/files", label: "nav.drive", icon: "folder" },
  { id: "meetings", href: "/meetings", label: "nav.meetings", icon: "calendar" }, // meetings
  { id: "skills", href: "/skills", label: "nav.skills", icon: "layers" },
  { id: "connections", href: "/connections", label: "nav.connections", icon: "plug" },
  { id: "shop", href: "/shop", label: "nav.shop", icon: "bag" },
  { id: "settings", href: "/settings", label: "nav.settings", icon: "settings" },
] as const;
export type Section = (typeof SECTIONS)[number];
export type SectionId = Section["id"] | "dashboard";

/** The grouped menu (proposal 5.2): what you do every day, your company, what agents know and use; extras at the bottom. */
export const NAV_GROUPS: { id: "day" | "company" | "teach" | "bottom"; label: string | null; items: Section["id"][] }[] = [
  { id: "day", label: "nav.group.day", items: ["office", "pending", "chats", "projects"] },
  { id: "company", label: "nav.group.company", items: ["org", "customers", "drive", "meetings"] },
  { id: "teach", label: "nav.group.teach", items: ["skills", "connections"] },
  { id: "bottom", label: null, items: ["shop", "settings"] },
];

/** The menu groups with their sections; the shop only where the rewards system is on. */
export function navGroups({ shopAvailable }: { shopAvailable: boolean }) {
  return NAV_GROUPS.map((g) => ({
    ...g,
    items: g.items.filter((id) => id !== "shop" || shopAvailable).map((id) => SECTIONS.find((s) => s.id === id)!),
  }));
}

export function sectionOf(pathname: string): SectionId {
  if (pathname.startsWith("/dashboard")) return "dashboard";
  if (pathname.startsWith("/approvals")) return "pending";
  if (pathname.startsWith("/chats")) return "chats";
  if (pathname.startsWith("/projects")) return "projects";
  if (pathname.startsWith("/org")) return "org"; // >>> org <<<
  if (pathname.startsWith("/clients")) return "customers";
  if (pathname.startsWith("/meetings")) return "meetings"; // meetings
  if (pathname.startsWith("/files")) return "drive";
  if (pathname.startsWith("/skills")) return "skills";
  if (pathname.startsWith("/connections")) return "connections";
  if (pathname.startsWith("/shop")) return "shop";
  if (pathname.startsWith("/settings")) return "settings";
  return "office";
}
