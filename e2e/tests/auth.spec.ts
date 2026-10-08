import { expect, test, type Page } from "@playwright/test";

/**
 * Accounts and login (A2). Needs a stack with AUTH_ENABLED=true, so it only runs with E2E_AUTH=1:
 *   E2E_AUTH=1 FRONTEND_URL=http://localhost:3000 npx playwright test auth.spec.ts
 * The default stack (open demo) keeps working without login; that path is covered by the other specs.
 */
test.skip(process.env.E2E_AUTH !== "1", "requires a stack with AUTH_ENABLED=true (E2E_AUTH=1)");

const pw = "Correct-Horse-9-e2e";
const uniq = () => Math.random().toString(36).slice(2, 8);

async function openAdmin(page: Page) {
  await page.getByTestId("admin-menu").click();
  await expect(page.getByTestId("admin-menu-list")).toBeVisible();
}

test("register, invite a member, accept the invitation and sign in again", async ({ page, browser }) => {
  const owner = `owner-${uniq()}@example.com`;
  const invitee = `member-${uniq()}@example.com`;

  // The app sends anonymous visitors to the login.
  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
  await page.getByTestId("auth-to-register").click();
  await page.getByTestId("auth-email").fill(owner);
  await page.getByTestId("auth-name").fill("Owner E2E");
  await page.getByTestId("auth-org").fill(`Org ${uniq()}`);
  await page.getByTestId("auth-password").fill(pw);
  await page.getByTestId("auth-submit").click();
  await expect(page).toHaveURL(/\/$/);

  // Invite from the admin menu and read the one-time link.
  await openAdmin(page);
  await expect(page.getByTestId("account-who")).toContainText(owner);
  await page.getByTestId("members-open").click();
  await page.getByTestId("invite-email").fill(invitee);
  await page.getByTestId("invite-role").selectOption("member");
  await page.getByTestId("invite-send").click();
  const link = await page.getByTestId("invite-link").inputValue();
  expect(link).toContain("/invite?token=");
  await expect(page.getByTestId("invite-row").first()).toHaveAttribute("data-status", "pending");

  // The invitee accepts in a fresh browser context.
  const other = await browser.newContext();
  const p2 = await other.newPage();
  await p2.goto(link);
  await p2.getByTestId("auth-name").fill("Member E2E");
  await p2.getByTestId("auth-password").fill(pw);
  await p2.getByTestId("auth-submit").click();
  await expect(p2).toHaveURL(/\/$/);
  await openAdmin(p2);
  await expect(p2.getByTestId("account-who")).toContainText(invitee);
  // A member cannot reset the demo (admin only).
  await expect(p2.getByTestId("demo-reset")).toHaveCount(0);

  // The link is single use.
  const p3 = await (await browser.newContext()).newPage();
  await p3.goto(link);
  await p3.getByTestId("auth-name").fill("Again");
  await p3.getByTestId("auth-password").fill(pw);
  await p3.getByTestId("auth-submit").click();
  await expect(p3.getByTestId("conn-error")).toBeVisible();

  // Sign out and back in.
  await p2.getByTestId("logout").click();
  await expect(p2).toHaveURL(/\/login$/);
  await p2.getByTestId("auth-email").fill(invitee);
  await p2.getByTestId("auth-password").fill(pw);
  await p2.getByTestId("auth-submit").click();
  await expect(p2).toHaveURL(/\/$/);
  await other.close();
});

test("wrong password shows an error and keeps the visitor on the login", async ({ page }) => {
  await page.goto("/login");
  await page.getByTestId("auth-email").fill(`nobody-${uniq()}@example.com`);
  await page.getByTestId("auth-password").fill("Wrong-Password-1");
  await page.getByTestId("auth-submit").click();
  await expect(page.getByTestId("conn-error")).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
});
