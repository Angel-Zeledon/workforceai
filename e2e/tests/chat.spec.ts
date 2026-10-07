import { test, expect } from "@playwright/test";
import { api, resetDemo } from "../support/api";

test.beforeEach(async () => {
  await resetDemo();
});

test("al tocar un agente el panel abre primero el chat 1:1", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("agent-accounting").click({ force: true });
  await expect(page.getByTestId("panel-tab-chat")).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("chat-input")).toBeVisible();
  await page.getByTestId("chat-input").fill("Hola");
  await page.getByTestId("chat-submit").click();
  // responde solo ese agente
  await expect(page.locator('[data-testid="chat-msg"]:not([data-from="user"])').first()).toBeVisible({ timeout: 30_000 });
  const others = await page.locator('[data-testid="chat-msg"]').evaluateAll((els) =>
    els.map((e) => e.getAttribute("data-from")).filter((f) => f !== "user" && f !== "accounting"),
  );
  expect(others).toEqual([]);
});

test("un saludo en la oficina es conversación: nadie crea una solicitud", async ({ page }) => {
  await page.goto("/");
  const before = ((await api.get<unknown[] | null>("/requests")) ?? []).length;
  await page.getByTestId("command-input").fill("Hola equipo, buenos días");
  await page.getByTestId("command-submit").click();
  await expect(page.getByTestId("turn-chip").first()).toHaveAttribute("data-intent", "smalltalk", { timeout: 30_000 });
  await expect(page.locator('[data-testid="chat-msg"]:not([data-from="user"])').first()).toBeVisible({ timeout: 30_000 });
  const after = ((await api.get<unknown[] | null>("/requests")) ?? []).length;
  expect(after).toBe(before);
});
