import { test, expect } from "@playwright/test";

test("el selector Lista | Oficina 3D cambia de vista y recuerda la última", async ({ page }) => {
  await page.goto("/");
  const toggle = page.getByTestId("mode-toggle");
  await expect(toggle).toBeVisible();
  await expect(page.getByTestId("mode-list")).toBeVisible();
  await expect(page.getByTestId("mode-3d")).toBeVisible();

  // Lista: el espacio de trabajo con la caja para escribir
  await page.getByTestId("mode-list").click();
  await expect(toggle).toHaveAttribute("data-mode", "dashboard");
  await expect(page.getByTestId("work-composer")).toBeVisible();
  await expect(page.getByTestId("work-input")).toBeVisible();

  // la vista queda recordada al recargar
  await page.reload();
  await expect(page.getByTestId("work-composer")).toBeVisible();

  // Oficina 3D: la escena vuelve, sin pasar por Ajustes
  await page.getByTestId("mode-3d").click();
  await expect(toggle).toHaveAttribute("data-mode", "office");
  await expect(page.getByTestId("command-input")).toBeVisible();
});
