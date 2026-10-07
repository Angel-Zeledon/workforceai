import { test, expect } from "@playwright/test";

test("el toggle cambia entre modo oficina y modo dashboard", async ({ page }) => {
  await page.goto("/");
  const toggle = page.getByTestId("mode-toggle");
  await expect(toggle).toBeVisible();

  const before = await page.evaluate(() => document.body.innerText);
  await toggle.click();

  // El modo activo debe quedar expuesto en el atributo data-mode del toggle.
  await expect(toggle).toHaveAttribute("data-mode", "dashboard");
  await expect(page.getByTestId("activity-feed")).toBeAttached();
  const after = await page.evaluate(() => document.body.innerText);
  expect(after).not.toEqual(before);

  await toggle.click();
  await expect(toggle).toHaveAttribute("data-mode", "office");
});
