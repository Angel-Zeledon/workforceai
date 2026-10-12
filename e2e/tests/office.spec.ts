import { test, expect } from "@playwright/test";
import { AGENT_IDS } from "../support/env";
import { resetDemo } from "../support/api";

test.beforeEach(async () => {
  await resetDemo();
});

test("la oficina carga y muestra la UI base", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("command-input")).toBeVisible();
  await expect(page.getByTestId("command-submit")).toBeVisible();
  await expect(page.getByTestId("mode-toggle")).toBeVisible();
  await expect(page.getByTestId("activity-feed")).toBeAttached();
});

test("aparecen los 7 personajes", async ({ page }) => {
  await page.goto("/");
  for (const id of AGENT_IDS) {
    await expect(page.getByTestId(`agent-${id}`)).toBeAttached();
  }
  // The 7 seed agents at least: other specs may have hired more (the demo reset keeps hires).
  expect(await page.locator('[data-testid^="agent-"]').count()).toBeGreaterThanOrEqual(AGENT_IDS.length);
});
