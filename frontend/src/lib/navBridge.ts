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

/** Sections of the app: route, i18n label key and icon. */
export const SECTIONS = [
  { id: "office", href: "/", label: "nav.office", icon: "office" },
  { id: "chats", href: "/chats", label: "nav.chats", icon: "chat" },
  { id: "projects", href: "/projects", label: "nav.projects", icon: "folder" },
  { id: "drive", href: "/files", label: "nav.drive", icon: "doc" },
  { id: "skills", href: "/skills", label: "nav.skills", icon: "layers" },
  { id: "shop", href: "/shop", label: "nav.shop", icon: "bag" },
] as const;
export type SectionId = (typeof SECTIONS)[number]["id"] | "dashboard" | "settings";

export function sectionOf(pathname: string): SectionId {
  if (pathname.startsWith("/dashboard")) return "dashboard";
  if (pathname.startsWith("/chats")) return "chats";
  if (pathname.startsWith("/projects")) return "projects";
  if (pathname.startsWith("/files")) return "drive";
  if (pathname.startsWith("/skills")) return "skills";
  if (pathname.startsWith("/shop")) return "shop";
  if (pathname.startsWith("/settings")) return "settings";
  return "office";
}
