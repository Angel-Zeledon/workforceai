import { test, expect, type Page } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { API_URL } from "../support/env";
import { api, pendingApprovals, resetDemo, sleep } from "../support/api";

/** G1: files in the conversation. Agents leave file cards in their messages, a side panel shows them by type,
 *  people edit or ask for changes, and attach their own files to a message. */

/** The stack may be busy (WebGL, other builds): a dropped keep-alive socket is retried, never a test failure. */
async function retry<T>(fn: () => Promise<T>, tries = 8): Promise<T> {
  for (let i = 0; ; i++) {
    try { return await fn(); } catch (e) { if (i >= tries) throw e; await sleep(1500); }
  }
}
const get = <T>(path: string) => retry(() => api.get<T>(path));

test.beforeEach(async () => {
  await retry(() => resetDemo());
});

// The office scene (WebGL) keeps the page busy while the team works: UI steps get a generous timeout.
test.use({ actionTimeout: 120_000 });
test.describe.configure({ timeout: 600_000 });
const T = { timeout: 120_000 };

interface DriveFileRow { id: string; name: string; path: string; current_version: number }
interface DriveListing { files: DriveFileRow[] }
interface ChatRow { from: string; text: string; files?: { path: string; name: string; file_id?: string; version?: number; action?: string }[] }

/** Sends the $50,000 request through the office chat and approves what the team asks for. */
async function runScenarioInChat(page: Page) {
  await page.goto("/");
  await page.getByTestId("command-input").fill(SCENARIO_TEXT);
  await page.getByTestId("command-submit").click();
  await expect.poll(async () => ((await get<{ id: string }[] | null>("/requests")) ?? []).length, { timeout: 90_000 }).toBeGreaterThan(0);
  const [req] = await get<{ id: string }[]>("/requests");
  await approveUntilDone(req.id);
}

/** Approves whatever the team asks for until the request is done. */
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

test("the agents' messages show a file card after the $50,000 request and the panel opens it as Markdown", async ({ page }) => {
  await runScenarioInChat(page);
  const card = page.getByTestId("file-card").first();
  await expect(card).toBeVisible({ timeout: 30_000 });
  await expect(card).toHaveAttribute("data-kind", "markdown", T);
  await expect(card).toContainText(".md", T);

  // the same reference is in the stored history: path and version, never content
  const msgs = await get<ChatRow[]>("/conversations/office/messages");
  const withFiles = msgs.filter((m) => m.files?.length);
  expect(withFiles.length).toBeGreaterThan(0);
  expect(withFiles[0].from).not.toBe("user");
  expect(withFiles[0].files![0].path).toMatch(/^\/Agents\/[^/]+\/.+\.md$/);
  expect(JSON.stringify(withFiles[0])).not.toContain("content");

  await card.click();
  const panel = page.getByTestId("file-viewer");
  await expect(panel).toBeVisible(T);
  await expect(panel).toHaveAttribute("data-kind", "markdown", T);
  await expect(page.getByTestId("viewer-markdown")).toBeVisible(T);
  await expect(page.getByTestId("viewer-version")).toContainText(/v\d+/, T);
  // Escape closes it
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0, T);
});

test("editing a Markdown file in the panel saves a new version (v2 on a fresh drive); a stale save shows the conflict", async ({ page }) => {
  await runScenarioInChat(page);
  const card = page.getByTestId("file-card").first();
  await expect(card).toBeVisible({ timeout: 30_000 });
  const path = (await card.getAttribute("data-path"))!;
  // the drive survives demo resets: start from whatever version the file has now
  const folder = path.split("/").slice(0, -1).join("/");
  const rowOf = async () => (await get<DriveListing>(`/drive/list?path=${encodeURIComponent(folder)}`)).files.find((f) => f.path === path)!;
  const v0 = (await rowOf()).current_version;
  await card.click();
  await page.getByTestId("viewer-edit").click();
  await page.getByTestId("viewer-textarea").fill("# Propuesta revisada\n\n- punto editado por una persona\n");
  await page.getByTestId("viewer-save").click();
  await expect(page.getByTestId("viewer-markdown")).toContainText("punto editado por una persona", T);
  await expect(page.getByTestId("viewer-version")).toContainText(`v${v0 + 1}`, T);

  // the drive agrees and keeps the previous versions
  const row = await rowOf();
  expect(row.current_version).toBe(v0 + 1);
  const versions = await get<{ versions: unknown[] }>(`/drive/files/${row.id}/versions`);
  expect(versions.versions).toHaveLength(v0 + 1);

  // someone else saves a newer version while the panel is editing -> 409 and a clear choice
  await page.getByTestId("viewer-edit").click();
  await page.getByTestId("viewer-textarea").fill("# Mi cambio\n");
  const res = await retry(() => fetch(`${API_URL}/drive/files/${row.id}/content?expected_version=${v0 + 1}`, {
    method: "PUT", headers: { "content-type": "text/plain" }, body: "# Cambio de otra persona\n",
  }));
  expect(res.status).toBe(200);
  await page.getByTestId("viewer-save").click();
  await expect(page.getByTestId("viewer-conflict")).toBeVisible(T);
  await page.getByTestId("viewer-conflict-overwrite").click();
  await expect(page.getByTestId("viewer-version")).toContainText(`v${v0 + 3}`, T);
  await expect(page.getByTestId("viewer-markdown")).toContainText("Mi cambio", T);
});

test("a person attaches a CSV to a chat message: it shows as a card, opens as a sortable table and the agents get its path", async ({ page }) => {
  await page.goto("/");
  const name = `saldos-${Date.now()}.csv`;
  await page.getByTestId("chat-attach-input").first().setInputFiles({
    name, mimeType: "text/csv", buffer: Buffer.from("cuenta,saldo\nOperativa,128400\nNómina,56300\nUSD,40200\n"),
  });
  await expect(page.getByTestId("chat-attachments")).toContainText(name, T);
  await page.getByTestId("command-input").fill("Hola equipo, revisen este archivo");
  await page.getByTestId("command-submit").click();

  const card = page.locator(`[data-testid=file-card][data-path$="/${name}"]`).first();
  await expect(card).toBeVisible({ timeout: 20_000 });
  await expect(card).toHaveAttribute("data-kind", "csv", T);

  // stored with the user's own text plus the file reference
  await expect.poll(async () => (await get<ChatRow[]>("/conversations/office/messages")).some((m) => m.from === "user" && m.files?.some((f) => f.path.endsWith(`/${name}`) && f.action === "attach")), { timeout: 20_000 }).toBe(true);
  const stored = (await get<ChatRow[]>("/conversations/office/messages")).find((m) => m.from === "user" && m.files?.length)!;
  expect(stored.text).toBe("Hola equipo, revisen este archivo");
  expect(stored.files![0].path).toMatch(/^\/Shared\/Chat\//);

  await card.click();
  await expect(page.getByTestId("viewer-table")).toContainText("Operativa", T);
  // sort by the numeric column: descending after two clicks
  await page.getByTestId("viewer-sort-1").click();
  await expect(page.getByTestId("viewer-table").locator("tbody tr").first()).toContainText("40200", T);
  await page.getByTestId("viewer-sort-1").click();
  await expect(page.getByTestId("viewer-table").locator("tbody tr").first()).toContainText("128400", T);
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
  await expect(page.getByTestId("viewer-ask-sent")).toBeVisible(T);
  await expect.poll(async () => {
    // the conversation is created by the first message: 404 until then
    const msgs = await api.get<ChatRow[]>(`/conversations/agent:${agent}/messages`).catch(() => [] as ChatRow[]);
    return msgs.some((m) => m.from === "user" && m.text.includes(path) && m.text.includes("agrega una sección de riesgos") && m.files?.some((f) => f.path === path));
  }, { timeout: 120_000 }).toBe(true);
  // and the agent takes it as work (a plan, approved here): its task finishes with its own file card in that 1:1 chat
  const findRequest = async () => ((await get<{ id: string; text: string }[] | null>("/requests")) ?? []).find((r) => r.text.includes("[attachments]") && r.text.includes(path));
  await expect.poll(async () => !!(await findRequest()), { timeout: 120_000 }).toBe(true);
  await approveUntilDone((await findRequest())!.id);
  await expect.poll(async () => {
    const msgs = await api.get<ChatRow[]>(`/conversations/agent:${agent}/messages`).catch(() => [] as ChatRow[]);
    return msgs.some((m) => m.from === agent && m.files?.some((f) => f.action === "write"));
  }, { timeout: 60_000 }).toBe(true);
});
