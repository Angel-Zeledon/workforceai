import { test, expect } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, resetDemo } from "../support/api";

/** E1: chat history. General (the whole office) and one thread per agent go hand in hand. */

test.beforeEach(async () => {
  await resetDemo();
});

test.use({ actionTimeout: 60_000 });
test.describe.configure({ timeout: 300_000 });

test("saludo y trabajo en la oficina: /chats los muestra en General, en el hilo del agente, y al recargar se puede seguir", async ({ page }) => {
  await page.goto("/");

  // a greeting (conversation) and a work request (creates a request), both through the office chat bar
  await page.getByTestId("command-input").fill("Hola equipo, buenos días");
  await page.getByTestId("command-submit").click();
  await expect(page.locator('[data-testid="chat-msg"]:not([data-from="user"])').first()).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("command-input").fill(SCENARIO_TEXT);
  await page.getByTestId("command-submit").click();
  await expect.poll(async () => ((await api.get<unknown[] | null>("/requests")) ?? []).length, { timeout: 60_000 }).toBeGreaterThan(0);

  // the history screen
  await page.goto("/chats");
  await expect(page.getByTestId("chats-view")).toBeVisible();
  await expect(page.getByTestId("chats-thread").first()).toHaveAttribute("data-thread", "general");

  // General shows both messages, chronologically, with agent badges on what you wrote
  const log = page.getByTestId("chats-log");
  await expect(log.locator('[data-testid="chats-msg"][data-from="user"]', { hasText: "Hola equipo, buenos días" })).toBeVisible({ timeout: 60_000 });
  const work = log.locator('[data-testid="chats-msg"][data-from="user"]', { hasText: SCENARIO_TEXT.slice(0, 30) });
  await expect(work).toBeVisible();
  const order = await log.locator('[data-testid="chats-msg"]').evaluateAll((els) => els.map((e) => e.textContent ?? ""));
  expect(order.findIndex((t) => t.includes("Hola equipo"))).toBeLessThan(order.findIndex((t) => t.includes(SCENARIO_TEXT.slice(0, 30))));
  await expect(work.locator('[data-testid="chats-badge"], [data-testid="route-note"]').first()).toBeVisible({ timeout: 60_000 });

  // the agent that took the work has the very same message in its own thread
  const badge = await work.getAttribute("data-badges");
  const agents = (badge ?? "").split(",").filter(Boolean);
  expect(agents.length).toBeGreaterThan(0);
  const agent = agents.find((a) => a !== "assistant") ?? agents[0];
  await page.getByTestId("chats-thread").and(page.locator(`[data-thread="${agent}"]`)).click();
  await expect(page).toHaveURL(new RegExp(`/chats/${agent}$`));
  await expect(page.getByTestId("chats-log").locator('[data-testid="chats-msg"][data-from="user"]', { hasText: SCENARIO_TEXT.slice(0, 30) })).toBeVisible({ timeout: 30_000 });
  // ...and only what concerns it: the greeting went to the others, not to a thread it did not take part in
  const mine = await page.getByTestId("chats-log").locator('[data-testid="chats-msg"]').evaluateAll((els) =>
    els.map((e) => [e.getAttribute("data-from"), e.getAttribute("data-badges")]));
  for (const [, b] of mine as [string, string][]) expect((b ?? "").split(",")).toContain(agent);

  // the request produced from the chat is linked from its message
  await page.getByTestId("chats-thread").first().click();
  await expect(page.getByTestId("chats-request").first()).toBeVisible({ timeout: 60_000 });

  // reload the agent's thread: the history is still there, and the conversation continues
  await page.goto(`/chats/${agent}`);
  await expect(page.getByTestId("chats-log").locator('[data-testid="chats-msg"][data-from="user"]', { hasText: SCENARIO_TEXT.slice(0, 30) })).toBeVisible({ timeout: 60_000 });
  const before = await page.getByTestId("chats-log").locator('[data-testid="chats-msg"]').count();
  await page.getByTestId("chats-input").fill("Gracias, sigo por aquí");
  await page.getByTestId("chats-send").click();
  await expect(page.getByTestId("chats-log").locator('[data-testid="chats-msg"][data-from="user"]', { hasText: "Gracias, sigo por aquí" })).toBeVisible();
  await expect.poll(async () => page.getByTestId("chats-log").locator('[data-testid="chats-msg"]').count(), { timeout: 60_000 }).toBeGreaterThan(before + 1);

  // what you said to the agent shows in General too (vice versa)
  await page.goto("/chats");
  await expect(page.getByTestId("chats-log").locator('[data-testid="chats-msg"][data-from="user"]', { hasText: "Gracias, sigo por aquí" })).toBeVisible({ timeout: 60_000 });

  // search finds the conversation by its content
  await page.getByTestId("chats-search").fill("Gracias, sigo");
  await expect(page.getByTestId("chats-results")).toBeVisible();
  await expect(page.getByTestId("chats-conv").first()).toBeVisible({ timeout: 30_000 });
});

test("el panel del agente enlaza a su historial de chat", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("agent-accounting").click({ force: true });
  await page.getByTestId("agent-chat-history").click();
  await expect(page).toHaveURL(/\/chats\/accounting$/);
  await expect(page.getByTestId("chats-title")).toBeVisible();
  await expect(page.getByTestId("chats-empty")).toBeVisible({ timeout: 30_000 });
});
