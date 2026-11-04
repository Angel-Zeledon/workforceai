import type { Page } from "@playwright/test";

/**
 * The avatar of the top bar (`admin-menu`, it replaced the gear) keeps: account-who, account-open, ctl-killswitch,
 * demo-reset, logout, onboarding-open (while the set-up is pending) and team-setup-open. The menu closes after each
 * choice. Opens it if needed.
 */
export async function openAdminMenu(page: Page) {
  if ((await page.getByTestId("admin-menu").getAttribute("aria-expanded")) !== "true") await page.getByTestId("admin-menu").click();
}

/** Entries that left the gear menu (UX tanda 1) and where they live now. */
const MOVED: Record<string, { href: string; testid?: string }> = {
  "nav-connections": { href: "/connections" },                       // main menu, "Conexiones"
  "nav-security": { href: "/settings", testid: "settings-security" },  // Ajustes → Seguridad
  "templates-open": { href: "/projects?view=library" },              // Proyectos → Biblioteca de plantillas (/settings?s=templates redirects)
  "hire-open": { href: "/settings?s=hire" },                         // Agentes y oficinas → Contratar agente
  "office-settings": { href: "/settings", testid: "settings-office" }, // Ajustes → Apariencia e idioma
  "org-config-open": { href: "/settings", testid: "settings-onboarding" }, // Ajustes → Tu negocio
  "members-open": { href: "/settings", testid: "settings-members" },   // Ajustes → Miembros
};

/** Click an admin entry by test id: in the avatar menu, or at its new home in Ajustes / the main menu. */
export async function adminClick(page: Page, testid: string) {
  const moved = MOVED[testid];
  if (moved) {
    await page.goto(moved.href);
    if (moved.testid) await page.getByTestId(moved.testid).click();
    return;
  }
  await openAdminMenu(page);
  await page.getByTestId(testid).click();
}

/**
 * The agent panel has 5 tabs in one row (`panel-tab-chat|work|can|knows|profile`, UX tanda 2). The old sections live
 * inside them (frontend/src/components/agent/panelTabs.ts) and their jump buttons keep the old `panel-tab-<section>` ids:
 * this opens the new tab first (and "Más" in simple mode), then jumps to the section (unfolding it if needed).
 */
const PANEL_PARENT: Record<string, string> = {
  state: "work", tasks: "work", reports: "work", files: "work", activity: "work", thoughts: "work", chats: "work", memory: "knows",
};
export async function openPanelTab(page: Page, tab: string) {
  const parent = PANEL_PARENT[tab] ?? tab;
  if (!(await page.getByTestId(`panel-tab-${parent}`).isVisible()) && (await page.getByTestId("panel-view-all").isVisible())) await page.getByTestId("panel-view-all").click();
  await page.getByTestId(`panel-tab-${parent}`).click();
  if (parent !== tab) await page.getByTestId(`panel-tab-${tab}`).click();
}
