import { test, expect } from "@playwright/test";
import { api, pendingApprovals, resetDemo, sleep } from "../support/api";

/** F4: real office files. Asking for an Excel must deliver an .xlsx (not the same .md): the file shows up in the
 *  chat, the viewer renders the table with its sheet, and the download is the original workbook.
 *  Run on a SIMULATION stack: the simulated employee builds the file from the request text. */

async function retry<T>(fn: () => Promise<T>, tries = 8): Promise<T> {
  for (let i = 0; ; i++) {
    try { return await fn(); } catch (e) { if (i >= tries) throw e; await sleep(1500); }
  }
}
const get = <T>(path: string) => retry(() => api.get<T>(path));

test.beforeEach(async () => { await retry(() => resetDemo()); });
test.use({ actionTimeout: 120_000 });
test.describe.configure({ timeout: 600_000 });
const T = { timeout: 120_000 };

interface ChatRow { from: string; files?: { path: string; name: string; file_id?: string }[] }

async function approveUntilDone(requestId: string) {
  const deadline = Date.now() + 300_000;
  for (;;) {
    for (const a of await retry(() => pendingApprovals())) await retry(() => api.post(`/approvals/${a.id}/decision`, { decision: "approve" }));
    const r = await get<{ status: string }>(`/requests/${requestId}`);
    if (r.status === "done") break;
    expect(r.status, "the request must not fail").not.toBe("failed");
    expect(Date.now(), "request done in time").toBeLessThan(deadline);
    await sleep(1000);
  }
}

test("asking for an Excel delivers a real .xlsx: card in the chat, table in the viewer, original file on download", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("command-input").fill("Hazme un Excel con 10 productos, precio y stock");
  await page.getByTestId("command-submit").click();
  await expect.poll(async () => ((await get<{ id: string }[] | null>("/requests")) ?? []).length, { timeout: 90_000 }).toBeGreaterThan(0);
  const [req] = await get<{ id: string }[]>("/requests");
  await approveUntilDone(req.id);

  // an .xlsx is attached to an agent message (references only, never content)
  const card = page.locator('[data-testid="file-card"][data-kind="sheet"]').first();
  await expect(card).toBeVisible({ timeout: 60_000 });
  await expect(card).toContainText(".xlsx", T);
  const msgs = await get<ChatRow[]>("/conversations/office/messages");
  const xl = msgs.flatMap((m) => m.files ?? []).find((f) => f.path.endsWith(".xlsx"));
  expect(xl, "an xlsx file reference in the chat").toBeTruthy();
  expect(xl!.path).toMatch(/^\/Agents\/[^/]+\/.+\.xlsx$/);

  // the viewer shows the table: header, 10 products and a totals row
  await card.click();
  const panel = page.getByTestId("file-viewer");
  await expect(panel).toBeVisible(T);
  await expect(panel).toHaveAttribute("data-kind", "sheet", T);
  const table = page.getByTestId("viewer-sheet");
  await expect(table).toBeVisible(T);
  await expect(table.locator("thead th").nth(1)).toContainText("Producto", T);
  await expect(table.locator("thead th").nth(2)).toContainText("Precio", T);
  await expect(table.locator("thead th").nth(3)).toContainText("Stock", T);
  await expect(table.locator("tbody tr")).toHaveCount(11, T); // 10 products + Total
  await expect(table.locator("tbody tr").first()).toContainText("Producto 01", T);
  await expect(table.locator("tbody tr").last()).toContainText("Total", T);
  await expect(table.locator("tbody tr").first()).toContainText("$", T); // currency formatting survives

  // the download is the original workbook (a zip with the OOXML parts), not a text rendering
  const [dl] = await Promise.all([page.waitForEvent("download"), page.getByTestId("viewer-download").click()]);
  expect(dl.suggestedFilename()).toMatch(/\.xlsx$/);
  const path = await dl.path();
  const fs = await import("node:fs");
  const bytes = fs.readFileSync(path!);
  expect(bytes.subarray(0, 4).toString("latin1")).toBe("PK\u0003\u0004");
  expect(bytes.includes(Buffer.from("xl/workbook.xml"))).toBeTruthy();
});

test("asking for a Word document delivers a .docx shown as a formatted document", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("command-input").fill("Prepara un documento de Word con el plan de incorporación del equipo");
  await page.getByTestId("command-submit").click();
  await expect.poll(async () => ((await get<{ id: string }[] | null>("/requests")) ?? []).length, { timeout: 90_000 }).toBeGreaterThan(0);
  const [req] = await get<{ id: string }[]>("/requests");
  await approveUntilDone(req.id);
  const card = page.locator('[data-testid="file-card"][data-kind="doc"]').first();
  await expect(card).toBeVisible({ timeout: 60_000 });
  await card.click();
  await expect(page.getByTestId("file-viewer")).toHaveAttribute("data-kind", "doc", T);
  const doc = page.getByTestId("viewer-doc");
  await expect(doc).toBeVisible(T);
  await expect(doc.locator("h2, h3").first()).toBeVisible(T);
});
