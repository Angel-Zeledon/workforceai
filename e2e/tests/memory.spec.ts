import { test, expect } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, approveUntilDone, resetDemo } from "../support/api";
import { openPanelTab } from "../support/ui";

/** M1: long-term memory. The first request leaves memories; a second, related request carries them
 * (as untrusted data with refs) in the agent's prompt and can search again with memory.search. */

interface MemoryEntry { id?: string; ref: string; kind: string; title: string; snippet: string; source_ref?: string; deletable: boolean }
interface TaskRow { id: string; request_id: string; agent_id: string; status: string }
interface Trace { id: string; task_id: string; tool_requests: { tool: string; action: string }[] }
interface TraceDetail extends Trace { prompt?: { segments: { kind: string; label: string; text: string }[] } }

const SECOND =
  "Otro cliente pidió una propuesta de $50,000. Prepara la propuesta, revisa el contrato, el margen y la rentabilidad con lo que ya hicimos antes. [sim:memory]";

test.beforeEach(async () => {
  await resetDemo();
  await api.put("/settings/traces", { store_prompts: true });
});

test("a second related request carries the memory of the first", async ({ page }) => {
  test.setTimeout(900_000); // the UI part is slow on a loaded machine (software WebGL)
  const first = await api.post<{ request_id: string }>("/requests", { text: SCENARIO_TEXT });
  expect((await approveUntilDone(first.request_id, 400_000)).status).toBe("done");

  // what the sales agent remembers: its work and the lesson it declared, with sources
  let mem: MemoryEntry[] = [];
  await expect
    .poll(async () => (mem = (await api.get<MemoryEntry[] | null>("/agents/sales/memory")) ?? []).length, { timeout: 30_000 })
    .toBeGreaterThanOrEqual(2);
  const note = mem.find((m) => m.kind === "note");
  expect(note, "a lesson note").toBeTruthy();
  expect(note!.source_ref).toMatch(/^task:/);
  expect(mem.some((m) => m.kind === "task" && m.ref.startsWith("task:"))).toBe(true);

  const second = await api.post<{ request_id: string }>("/requests", { text: SECOND });
  expect((await approveUntilDone(second.request_id, 400_000)).status).toBe("done");

  // the second request's tasks: at least one run carries the retrieved memory, with refs, as untrusted data
  const tasks = ((await api.get<TaskRow[] | null>("/tasks")) ?? []).filter((t) => t.request_id === second.request_id);
  expect(tasks.length).toBeGreaterThan(0);
  let withMemory = 0;
  let searched = 0;
  for (const t of tasks) {
    const { traces } = await api.get<{ traces: Trace[] }>(`/agents/${t.agent_id}/traces?limit=50`);
    for (const tr of traces.filter((x) => x.task_id === t.id)) {
      if (tr.tool_requests.some((q) => q.tool === "memory" && q.action === "search")) searched++;
      const d = await api.get<TraceDetail>(`/agents/${t.agent_id}/traces/${tr.id}`);
      const seg = d.prompt?.segments.find((s) => s.kind === "untrusted" && /memoria relevante/i.test(s.label));
      if (seg && /task:|memory:|report:/.test(seg.text)) withMemory++;
    }
  }
  expect(withMemory, "runs whose prompt carried recalled memory").toBeGreaterThan(0);
  expect(searched, "memory.search requests of the simulated employees").toBeGreaterThan(0);

  // the Memory tab lists it with its source; searching works; forgetting is audited
  await page.goto("/");
  await page.getByTestId("agent-sales").click({ force: true });
  await openPanelTab(page, "memory");
  await expect(page.getByTestId("memory-tab")).toBeVisible();
  await expect(page.getByTestId("memory-entry-note").first()).toBeVisible({ timeout: 150_000 });
  await expect(page.getByTestId("memory-source").first()).toContainText(/task:/);

  await page.getByTestId("memory-search").fill("propuesta contrato margen");
  await page.getByTestId("memory-search-go").click();
  await expect(page.locator('[data-testid^="memory-entry-"]').first()).toBeVisible({ timeout: 150_000 });

  await page.getByTestId("memory-search").fill("");
  await expect(page.getByTestId("memory-entry-note").first()).toBeVisible({ timeout: 150_000 });
  const before = await page.getByTestId("memory-entry-note").count();
  await page.getByTestId("memory-entry-note").first().getByTestId("memory-delete").click();
  await expect(page.getByTestId("memory-entry-note")).toHaveCount(before - 1);
  // the keep-alive socket of the API client may have been closed while the slow UI part ran: retry once
  const getAudit = () => api.get<{ items?: { action: string }[] } | { action: string }[]>("/audit?action=memory.deleted");
  const audit = await getAudit().catch(getAudit);
  const rows = Array.isArray(audit) ? audit : (audit.items ?? []);
  expect(rows.some((r) => r.action === "memory.deleted")).toBe(true);
});

test("an agent never recalls a file it may not read", async () => {
  const put = (path: string, text: string) =>
    fetch(`${process.env.API_URL ?? "http://localhost:8080/api/v1"}/drive/upload?path=${encodeURIComponent(path)}`, { method: "PUT", body: text, headers: { "content-type": "text/markdown" } });
  const a = await put("/Agents/legal/secreto.md", "clave zafiro del escritorio de legal");
  const b = await put("/Shared/politica-zafiro.md", "política compartida zafiro");
  test.skip(!a.ok || !b.ok, "drive upload endpoint not available in this stack");
  const mine = (await api.get<MemoryEntry[] | null>("/agents/sales/memory?q=zafiro")) ?? [];
  const refs = mine.map((m) => m.ref);
  expect(refs).toContain("file:/Shared/politica-zafiro.md");
  expect(refs).not.toContain("file:/Agents/legal/secreto.md");
});
