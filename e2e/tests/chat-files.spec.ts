import { test, expect, type Page } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, approveUntilDone, resetDemo, sleep } from "../support/api";

/** G1: files in the conversation. Agents leave file cards in their messages, a side panel shows them by type,
 *  people edit or ask for changes, and attach their own files to a message. */

test.beforeEach(async () => {
  await resetDemo();
});

interface DriveFileRow { id: string; name: string; path: string; current_version: number }
interface DriveListing { files: DriveFileRow[] }
interface ChatRow { from: string; text: string; files?: { path: string; name: string; file_id?: string; version?: number; action?: string }[] }

/** Sends the $50,000 request through the office chat and approves what the team asks for. */
async function runScenarioInChat(page: Page) {
  await page.goto("/");
  await page.getByTestId("command-input").fill(SCENARIO_TEXT);
  await page.getByTestId("command-submit").click();
  await expect.poll(async () => ((await api.get<{ id: string }[] | null>("/requests")) ?? []).length, { timeout: 60_000 }).toBeGreaterThan(0);
  const [req] = (await api.get<{ id: string }[]>("/requests"));
  const done = await approveUntilDone(req.id);
  expect(done.status).toBe("done");
}

test("the agents' messages show a file card after the $50,000 request and the panel opens it as Markdown", async ({ page }) => {
  await runScenarioInChat(page);
  const card = page.getByTestId("file-card").first();
  await expect(card).toBeVisible({ timeout: 30_000 });
  await expect(card).toHaveAttribute("data-kind", "markdown");
  await expect(card).toContainText(".md");

  // the same reference is in the stored history: path and version, never content
  const msgs = await api.get<ChatRow[]>("/conversations/office/messages");
  const withFiles = msgs.filter((m) => m.files?.length);
  expect(withFiles.length).toBeGreaterThan(0);
  expect(withFiles[0].from).not.toBe("user");
  expect(withFiles[0].files![0].path).toMatch(/^\/Agents\/[^/]+\/.+\.md$/);
  expect(JSON.stringify(withFiles[0])).not.toContain("content");

  await card.click();
  const panel = page.getByTestId("file-viewer");
  await expect(panel).toBeVisible();
  await expect(panel).toHaveAttribute("data-kind", "markdown");
  await expect(page.getByTestId("viewer-markdown")).toBeVisible();
  await expect(page.getByTestId("viewer-version")).toContainText("v1");
  // Escape closes it
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
});

test("editing a Markdown file in the panel saves version 2; a stale save shows the conflict", async ({ page }) => {
  await runScenarioInChat(page);
  const card = page.getByTestId("file-card").first();
  await expect(card).toBeVisible({ timeout: 30_000 });
  const path = (await card.getAttribute("data-path"))!;
  await card.click();
  await page.getByTestId("viewer-edit").click();
  await page.getByTestId("viewer-textarea").fill("# Propuesta revisada\n\n- punto editado por una persona\n");
  await page.getByTestId("viewer-save").click();
  await expect(page.getByTestId("viewer-markdown")).toContainText("punto editado por una persona");
  await expect(page.getByTestId("viewer-version")).toContainText("v2");

  // the drive agrees and keeps v1
  const folder = path.split("/").slice(0, -1).join("/");
  const list = await api.get<DriveListing>(`/drive/list?path=${encodeURIComponent(folder)}`);
  const row = list.files.find((f) => f.path === path)!;
  expect(row.current_version).toBe(2);
  const versions = await api.get<{ versions: unknown[] }>(`/drive/files/${row.id}/versions`);
  expect(versions.versions).toHaveLength(2);

  // someone else saves v3 while the panel is editing v2 -> 409 and a clear choice
  await page.getByTestId("viewer-edit").click();
  await page.getByTestId("viewer-textarea").fill("# Mi cambio\n");
  const res = await fetch(`${(await import("../support/env")).API_URL}/drive/files/${row.id}/content?expected_version=2`, {
    method: "PUT", headers: { "content-type": "text/plain" }, body: "# Cambio de otra persona\n",
  });
  expect(res.status).toBe(200);
  await page.getByTestId("viewer-save").click();
  await expect(page.getByTestId("viewer-conflict")).toBeVisible();
  await page.getByTestId("viewer-conflict-overwrite").click();
  await expect(page.getByTestId("viewer-version")).toContainText("v4");
  await expect(page.getByTestId("viewer-markdown")).toContainText("Mi cambio");
});

test("a person attaches a CSV to a chat message: it shows as a card, opens as a sortable table and the agents get its path", async ({ page }) => {
  await page.goto("/");
  const name = `saldos-${Date.now()}.csv`;
  await page.getByTestId("chat-attach-input").first().setInputFiles({
    name, mimeType: "text/csv", buffer: Buffer.from("cuenta,saldo\nOperativa,128400\nNómina,56300\nUSD,40200\n"),
  });
  await expect(page.getByTestId("chat-attachments")).toContainText(name);
  await page.getByTestId("command-input").fill("Hola equipo, revisen este archivo");
  await page.getByTestId("command-submit").click();

  const card = page.locator(`[data-testid=file-card][data-path$="/${name}"]`).first();
  await expect(card).toBeVisible({ timeout: 20_000 });
  await expect(card).toHaveAttribute("data-kind", "csv");

  // stored with the user's own text plus the file reference
  await expect.poll(async () => (await api.get<ChatRow[]>("/conversations/office/messages")).some((m) => m.from === "user" && m.files?.some((f) => f.path.endsWith(`/${name}`) && f.action === "attach")), { timeout: 20_000 }).toBe(true);
  const stored = (await api.get<ChatRow[]>("/conversations/office/messages")).find((m) => m.from === "user" && m.files?.length)!;
  expect(stored.text).toBe("Hola equipo, revisen este archivo");
  expect(stored.files![0].path).toMatch(/^\/Shared\/Chat\//);

  await card.click();
  await expect(page.getByTestId("viewer-table")).toContainText("Operativa");
  // sort by the numeric column: descending after two clicks
  await page.getByTestId("viewer-sort-1").click();
  await expect(page.getByTestId("viewer-table").locator("tbody tr").first()).toContainText("40200");
  await page.getByTestId("viewer-sort-1").click();
  await expect(page.getByTestId("viewer-table").locator("tbody tr").first()).toContainText("128400");
});

test("asking for changes sends a message to the agent that references the file", async ({ page }) => {
  await runScenarioInChat(page);
  const card = page.getByTestId("file-card").first();
  await expect(card).toBeVisible({ timeout: 30_000 });
  const path = (await card.getAttribute("data-path"))!;
  const agent = path.split("/")[2];
  await card.click();
  await page.getByTestId("viewer-ask-input").fill("agrega una sección de riesgos");
  await page.getByTestId("viewer-ask-send").click();
  await expect(page.getByTestId("viewer-ask-sent")).toBeVisible();
  await expect.poll(async () => {
    const msgs = await api.get<ChatRow[]>(`/conversations/${encodeURIComponent(`agent:${agent}`)}/messages`);
    return msgs.some((m) => m.from === "user" && m.text.includes(path) && m.text.includes("agrega una sección de riesgos") && m.files?.some((f) => f.path === path));
  }, { timeout: 20_000 }).toBe(true);
  await sleep(100);
});
