import { test, expect } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, resetDemo } from "../support/api";

interface Report {
  id: string;
  title: string;
  contributors: string[];
}

test.beforeEach(async () => {
  await resetDemo();
});

test("solicitud -> aprobación -> reporte", async ({ page }) => {
  await page.goto("/");

  await page.getByTestId("command-input").fill(SCENARIO_TEXT);
  await page.getByTestId("command-submit").click();

  // Debe aparecer al menos una aprobación pendiente (p. ej. send_proposal).
  const approvals = page.locator('[data-testid^="approval-"]');
  await expect(approvals.first()).toBeVisible({ timeout: 90_000 });

  const approveBtn = () => approvals.getByRole("button", { name: /aprobar|approve/i });

  // Aprueba desde la UI cada aprobación que aparezca hasta que exista un reporte.
  await expect
    .poll(
      async () => {
        if ((await approveBtn().count()) > 0) {
          await approveBtn().first().click().catch(() => undefined);
        }
        const reports = (await api.get<Report[] | null>("/reports")) ?? [];
        return reports.length;
      },
      { timeout: 150_000, intervals: [1000] },
    )
    .toBeGreaterThan(0);

  const reports = (await api.get<Report[] | null>("/reports")) ?? [];
  expect(reports[0].contributors.length).toBeGreaterThanOrEqual(3);

  await expect(page.getByTestId("activity-feed")).toContainText(/report|reporte/i);
});
