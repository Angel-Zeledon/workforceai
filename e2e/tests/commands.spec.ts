import { test, expect, type Page } from "@playwright/test";
import { resetDemo } from "../support/api";

/**
 * K2: slash commands in the chat composers. Written against the real backend contract
 * (GET /commands, POST /commands/run, ...); it passes once the commands backend (K1) is merged.
 * Commands are accepted in Spanish AND English whatever the UI locale; the reply language follows the alias typed.
 */

test.beforeEach(async () => {
  await resetDemo();
});

test.use({ actionTimeout: 30_000 });
test.describe.configure({ timeout: 180_000 });

const input = (page: Page) => page.getByTestId("command-input");

/** Type a command and send it; a destructive one asks for confirmation first. */
async function run(page: Page, text: string, confirm = false) {
  await input(page).fill(text);
  // the menu may still be open on an exact name: Enter runs it, the button always does
  await page.getByTestId("command-submit").click();
  if (confirm) {
    await expect(page.getByTestId("cmd-confirm")).toBeVisible();
    await page.getByTestId("cmd-confirm-ok").click();
  }
}

test("el menu / se abre, busca en los dos idiomas, y Esc lo cierra", async ({ page }) => {
  await page.goto("/");
  await input(page).fill("/");
  await expect(page.getByTestId("cmd-menu")).toBeVisible();
  expect(await page.getByTestId("cmd-option").count()).toBeGreaterThan(10);

  await input(page).fill("/resum");
  await expect(page.getByTestId("cmd-option").first()).toBeVisible();
  await expect(page.locator('[data-testid="cmd-option"][data-key="compact"]')).toBeVisible();
  await input(page).fill("/compa");
  await expect(page.locator('[data-testid="cmd-option"][data-key="compact"]')).toBeVisible();

  await input(page).press("Escape");
  await expect(page.getByTestId("cmd-menu")).toBeHidden();
});

test("/limpiar, /resumir, /uso, /excel y /reunion devuelven su tarjeta en el chat", async ({ page }) => {
  await page.goto("/");

  await run(page, "/uso");
  await expect(page.locator('[data-testid="cmd-card"][data-command="usage"]')).toBeVisible({ timeout: 60_000 });

  await run(page, "/resumir");
  await expect(page.locator('[data-testid="cmd-card"][data-command="compact"]')).toBeVisible({ timeout: 90_000 });

  await run(page, "/excel ventas por mes de 2025");
  await expect(page.locator('[data-testid="cmd-card"][data-command="excel"]')).toBeVisible({ timeout: 90_000 });

  await run(page, "/reunion lanzamiento del producto de otoño");
  await expect(page.locator('[data-testid="cmd-card"][data-command="meeting"]')).toBeVisible({ timeout: 90_000 });

  // clear is destructive: the backend may ask for confirmation first
  await input(page).fill("/limpiar");
  await page.getByTestId("command-submit").click();
  const confirm = page.getByTestId("cmd-confirm");
  await expect(confirm.or(page.locator('[data-testid="cmd-card"][data-command="clear"]'))).toBeVisible({ timeout: 30_000 });
  if (await confirm.isVisible()) await page.getByTestId("cmd-confirm-ok").click();
  await expect(page.locator('[data-testid="cmd-card"][data-command="clear"]')).toBeVisible({ timeout: 30_000 });
});

test("el mismo comando funciona con alias en español y en inglés, y la respuesta sigue el alias", async ({ page }) => {
  await page.goto("/");

  const bodyOf = async (text: string) => {
    const req = page.waitForRequest((r) => r.url().includes("/commands/run") && r.method() === "POST");
    await run(page, text);
    return (await req).postDataJSON() as { command: string; lang: string };
  };

  const es = await bodyOf("/resumir");
  expect(es.command).toBe("compact");
  expect(es.lang).toBe("es");
  await expect(page.locator('[data-testid="cmd-card"][data-command="compact"]').first()).toBeVisible({ timeout: 90_000 });

  const en = await bodyOf("/compact");
  expect(en.command).toBe("compact");
  expect(en.lang).toBe("en");
  await expect(page.locator('[data-testid="cmd-card"][data-command="compact"]')).toHaveCount(2, { timeout: 90_000 });

  // names shared by both languages use the UI locale (whatever the browser picked)
  const uiLang = (await page.getByTestId("nav-office").innerText()).includes("Oficina") ? "es" : "en";
  const x = await bodyOf("/excel ventas");
  expect(x.command).toBe("excel");
  expect(x.lang).toBe(uiLang);
});

test("/ayuda y el boton ? abren el panel de comandos con buscador", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("cmd-help").first().click();
  await expect(page.getByTestId("cmd-help-panel")).toBeVisible();
  await page.getByTestId("cmd-help-search").fill("excel");
  await expect(page.locator('[data-testid="cmd-help-item"][data-cmd="excel"]')).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("cmd-help-panel")).toBeHidden();

  await input(page).fill("/ayuda");
  await page.getByTestId("command-submit").click();
  await expect(page.getByTestId("cmd-help-panel")).toBeVisible();
});

test("un comando desconocido se explica sin enviarse al chat", async ({ page }) => {
  await page.goto("/");
  await input(page).fill("/nosequees");
  await page.getByTestId("command-submit").click();
  await expect(page.getByTestId("cmd-error")).toBeVisible();
});
