import { test, expect } from "@playwright/test";
import { api, resetDemo, sleep } from "../support/api";

/**
 * A multinational project end to end on the real stack (simulated runtime): a group
 * consolidated close of 4 subsidiaries runs in parallel, a person approves the CFO
 * gate, and every agent task leaves its deliverable as a file in the project folder.
 */

interface ProjectSummary { id: string; name: string; status: string; tasks_done: number; tasks_total: number; failed?: number }
interface Listing { folders: { name: string; path: string }[]; files: { name: string; updated_by: { kind: string; id: string } }[] }

test.beforeEach(async () => {
  await resetDemo();
});

test("group consolidated close: parallel subsidiaries, CFO gate, deliverables in the drive", async ({ page }) => {
  test.setTimeout(300_000);
  const { project_id } = await api.post<{ project_id: string }>("/projects/from-template", {
    template_id: "group_consolidated_close", params: { subsidiaries: "4" }, locale: "es",
  });
  // the project asks for its data first: the sample data stands in for it
  await api.post(`/projects/${project_id}/inputs/demo`);
  await api.post(`/projects/${project_id}/launch`, { approved_budget_usd: 100, acknowledge_underbudget: true, max_parallel: 12 });

  let p: ProjectSummary | null = null;
  const deadline = Date.now() + 240_000;
  while (Date.now() < deadline) {
    for (const a of (await api.get<{ id: string }[] | null>("/approvals?status=pending")) ?? []) {
      await api.post(`/approvals/${a.id}/decision`, { decision: "approve", comment: "CFO" });
    }
    p = (await api.get<{ project: ProjectSummary }>(`/projects/${project_id}`)).project;
    if (["done", "failed", "cancelled"].includes(p.status)) break;
    await sleep(2000);
  }
  expect(p?.status).toBe("done");
  expect(p?.failed ?? 0).toBe(0);
  expect(p?.tasks_done).toBe(p?.tasks_total);

  // 4 subsidiaries x 4 tasks + 6 group tasks, each written by its agent into the project folder
  const projects = await api.get<Listing>(`/drive/list?path=${encodeURIComponent("/Projects")}`);
  // another project with the same name (an earlier run: the demo reset keeps the drive) makes the
  // drive disambiguate this one's folder with the end of its id
  const folder = projects.folders.find((f) => f.name === `${p?.name} (${project_id.slice(-6)})`)
    ?? projects.folders.find((f) => f.name === p?.name);
  expect(folder).toBeTruthy();
  const files = (await api.get<Listing>(`/drive/list?path=${encodeURIComponent(folder!.path)}`)).files;
  const byAgent = files.filter((f) => f.updated_by.kind === "agent");
  expect(byAgent.length).toBeGreaterThanOrEqual(4 * 4 + 6);
  expect(files.some((f) => f.name.startsWith("cons-"))).toBeTruthy();

  // and a person can open them in Files
  await page.goto("/");
  await page.getByTestId("nav-drive").click();
  await page.getByTestId("drive-folder-Projects").click();
  await page.getByTestId(`drive-folder-${folder!.name}`).click();
  await expect(page.locator('[data-testid^="drive-file-cons-"]')).toBeVisible();
});
