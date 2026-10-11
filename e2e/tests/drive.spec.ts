import { test, expect } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, approveUntilDone, resetDemo } from "../support/api";

/** F1/F2: the office drive. People upload and browse; agents keep their work as files on their desk. */

test.beforeEach(async () => {
  await resetDemo();
});

test("a person uploads a CSV to Shared and previews it", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("nav-drive").click();
  await expect(page.getByTestId("drive-view")).toBeVisible();
  for (const root of ["Agents", "Projects", "Clients", "Shared"]) await expect(page.getByTestId(`drive-folder-${root}`)).toBeVisible();

  await page.getByTestId("drive-folder-Shared").click();
  const name = `saldos-${Date.now()}.csv`;
  await page.locator('[data-testid="drive-view"] input[type=file]').setInputFiles({
    name, mimeType: "text/csv", buffer: Buffer.from("cuenta,saldo\nOperativa,128400\nNómina,56300\n"),
  });
  await page.getByTestId(`drive-file-${name}`).click();
  await expect(page.getByTestId("drive-preview-table")).toContainText("Operativa");
  await expect(page.getByTestId("drive-versions")).toContainText("v1");
});

test("after the $50,000 request each participating agent has its draft on its desk", async ({ page }) => {
  const { request_id } = await api.post<{ request_id: string }>("/requests", { text: SCENARIO_TEXT });
  const done = await approveUntilDone(request_id);
  expect(done.status).toBe("done");

  await page.goto("/");
  await page.getByTestId("nav-drive").click();
  await page.getByTestId("drive-folder-Agents").click();
  await page.getByTestId("drive-folder-legal").click();
  // the desk also holds the role's work kit (written by the system); the draft is the row the AGENT wrote
  const draft = page.locator('[data-testid^="drive-file-"]').filter({ hasText: /Agente|Agent/ }).first();
  await expect(draft).toBeVisible();
  await expect(draft).toContainText(".md");
  await draft.click();
  await expect(page.getByTestId("drive-file-pane")).toContainText(/Agente|Agent/);
  await expect(page.getByTestId("drive-preview-text")).toContainText("#");
  // and the work kit of the role is there too
  await expect(page.getByTestId("drive-file-checklist-revision-contrato.md")).toBeVisible();
});
