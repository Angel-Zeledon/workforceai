import type { Page } from "@playwright/test";

/**
 * Admin entries (nav-connections, nav-security, ctl-killswitch, templates-open,
 * onboarding-open, org-config-open, office-settings, demo-reset) live inside the
 * gear menu (`admin-menu`); the menu closes after each choice. Opens it if needed.
 */
export async function openAdminMenu(page: Page) {
  if ((await page.getByTestId("admin-menu").getAttribute("aria-expanded")) !== "true") await page.getByTestId("admin-menu").click();
}

/** Click an admin entry by test id (opens the gear menu first). */
export async function adminClick(page: Page, testid: string) {
  await openAdminMenu(page);
  await page.getByTestId(testid).click();
}

/**
 * The agent panel tabs (`panel-tab-*`) are the items of a radial wheel that is closed by
 * default (they stay in the DOM, so attribute checks work). To click one, open the wheel first.
 */
export async function openPanelTab(page: Page, tab: string) {
  if ((await page.getByTestId("panel-nav").getAttribute("aria-expanded")) !== "true") await page.getByTestId("panel-nav").click();
  await page.getByTestId(`panel-tab-${tab}`).click();
}
